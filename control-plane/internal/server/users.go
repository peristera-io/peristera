package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

var (
	errBadEmail       = errors.New("email must be a valid address")
	errNoTenant       = errors.New("no such tenant")
	errNotProvisioned = errors.New("tenant is not provisioned yet (no issuer)")
)

// createTenantUser creates a human user (login = email) as an ORG_OWNER in the
// tenant's own Zitadel instance and returns the login + a generated one-time
// password. Shared by the API and the UI so both hand the operator a password
// directly — no Secret to fish out with kubectl, and the same path serves
// lost-login recovery. The password is never persisted.
func (s *Server) createTenantUser(ctx context.Context, slug, email string) (login, password string, err error) {
	email = strings.TrimSpace(email)
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return "", "", errBadEmail
	}

	t := &v1alpha1.Tenant{}
	if err := s.K8s.Get(ctx, client.ObjectKey{Name: slug}, t); err != nil {
		if apierrors.IsNotFound(err) {
			return "", "", errNoTenant
		}
		return "", "", err
	}
	// The user lives in the tenant's own instance, reached at its issuer; until
	// the tenant is provisioned there is no instance to create it in.
	issuer := t.Status.Issuer
	if issuer == "" {
		return "", "", errNotProvisioned
	}

	orgID, err := s.IAM.FirstOrgID(ctx, issuer)
	if err != nil {
		return "", "", err
	}
	password, err = genPassword()
	if err != nil {
		return "", "", err
	}
	userID, err := s.IAM.CreateHumanUser(ctx, issuer, orgID, email, local, "Admin", password)
	if err != nil {
		return "", "", err // includes zitadel.ErrUserExists for the caller to map to 409
	}
	// Make them a tenant admin so they can manage their own users in the tenant
	// Zitadel console — the interim until the tenant dashboard (#53).
	if err := s.IAM.AddOrgMember(ctx, issuer, orgID, userID, []string{"ORG_OWNER"}); err != nil {
		return "", "", err
	}
	return email, password, nil
}

// genPassword returns a random password satisfying Zitadel's default
// complexity policy (upper, lower, digit, symbol).
func genPassword() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "Aa1!" + base64.RawURLEncoding.EncodeToString(raw), nil
}
