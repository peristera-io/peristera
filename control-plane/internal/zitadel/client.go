// Package zitadel is the control plane's client for the Zitadel System,
// Admin, and Management APIs — exactly the calls the M1 spike proved
// (ADR-0006 §5–6). It authenticates as a configured system user with a
// self-signed RS256 JWT whose audience is ALWAYS the deployment's
// ExternalDomain issuer, even for calls addressed to a tenant instance.
package zitadel

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned where "already gone" is a meaningful answer
// (instance deletion after projection lag, searches with no hit).
var ErrNotFound = errors.New("zitadel: not found")

type Client struct {
	// BaseURL is the deployment issuer (http://iam.…:9080) — System API
	// host and the fixed JWT audience.
	BaseURL string
	UserID  string

	key  *rsa.PrivateKey
	http *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

func NewFromKeyFile(baseURL, userID, keyPath string) (*Client, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", keyPath)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", keyPath, err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an RSA key", keyPath)
	}
	return &Client{
		BaseURL: baseURL, UserID: userID, key: key,
		http: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (c *Client) bearer() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.exp.Add(-2*time.Minute)) {
		return c.token, nil
	}
	now := time.Now()
	head := b64url([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iss": c.UserID, "sub": c.UserID, "aud": c.BaseURL,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	signing := head + "." + b64url(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	c.token, c.exp = signing+"."+b64url(sig), now.Add(time.Hour)
	return c.token, nil
}

// do performs one JSON call. url is absolute (tenant-instance calls use
// the tenant's own host); orgID sets the x-zitadel-orgid context header.
func (c *Client) do(ctx context.Context, method, url, orgID string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	tok, err := c.bearer()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	if orgID != "" {
		req.Header.Set("x-zitadel-orgid", orgID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s %s: %s", ErrNotFound, method, url, raw)
	case resp.StatusCode >= 300:
		ae := &apiError{method: method, url: url, status: resp.StatusCode, body: string(raw)}
		var parsed struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			ae.message = parsed.Message // Zitadel's typed error key
		}
		return ae
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// apiError carries the parsed Zitadel error so idempotency checks can key
// off the typed message field (not the whole formatted string, which
// includes the request URL — a data-influenced value).
type apiError struct {
	method, url, body, message string
	status                     int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s %s: %d: %s", e.method, e.url, e.status, e.body)
}

// CreateInstance creates a tenant's virtual instance with a machine org
// owner (humans are invited later, through the product). Returns the
// instance ID.
func (c *Client) CreateInstance(ctx context.Context, name, customDomain, orgName string) (string, error) {
	var out struct {
		InstanceID string `json:"instanceId"`
	}
	err := c.do(ctx, http.MethodPost, c.BaseURL+"/system/v1/instances/_create", "", map[string]any{
		"instanceName": name,
		"firstOrgName": orgName,
		"customDomain": customDomain,
		"machine": map[string]any{
			"userName": "org-admin",
			"name":     "Organization Admin",
		},
	}, &out)
	return out.InstanceID, err
}

// InstanceIDByDomain finds an existing instance by (custom) domain —
// the adoption path when Tenant status was lost. Returns ErrNotFound.
func (c *Client) InstanceIDByDomain(ctx context.Context, domain string) (string, error) {
	var out struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	err := c.do(ctx, http.MethodPost, c.BaseURL+"/system/v1/instances/_search", "", map[string]any{
		"queries": []any{map[string]any{
			"domainQuery": map[string]any{
				"domains": []string{domain},
			},
		}},
	}, &out)
	if err != nil {
		return "", err
	}
	if len(out.Result) == 0 {
		return "", ErrNotFound
	}
	return out.Result[0].ID, nil
}

func (c *Client) DeleteInstance(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, c.BaseURL+"/system/v1/instances/"+id, "", nil, nil)
}

// AddTrustedDomain lets the shared Login v2 (which calls the API under
// the deployment's ExternalDomain) serve this instance. Idempotent: an
// already-exists answer is swallowed (Zitadel phrases it "AlreadyExists").
func (c *Client) AddTrustedDomain(ctx context.Context, tenantBase, instanceID, domain string) error {
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/v2beta/instances/%s/trusted-domains", tenantBase, instanceID), "",
		map[string]any{"domain": domain}, nil)
	if err != nil && isAlreadyExists(err) {
		return nil
	}
	return err
}

// isAlreadyExists reports whether err is Zitadel's already-exists error,
// keyed off the typed message field only (e.g.
// "Errors.Instance.Domain.AlreadyExists") — not the formatted string, so a
// domain value that happens to contain "already exists" can't false-match
// (closes issue #8 for this path).
func isAlreadyExists(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.Contains(strings.ToLower(ae.message), "alreadyexists")
}

// FirstOrgID returns the instance's first organization (created with it).
func (c *Client) FirstOrgID(ctx context.Context, tenantBase string) (string, error) {
	var out struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, tenantBase+"/admin/v1/orgs/_search", "", map[string]any{}, &out); err != nil {
		return "", err
	}
	if len(out.Result) == 0 {
		return "", ErrNotFound
	}
	return out.Result[0].ID, nil
}

// EnsureStubApp makes sure the tenant's project and PKCE app exist and
// returns the OIDC client ID. Idempotent via search-by-name.
func (c *Client) EnsureStubApp(ctx context.Context, tenantBase, orgID string, redirectURIs, postLogoutURIs []string) (string, error) {
	return c.EnsureWebApp(ctx, tenantBase, orgID, "stub", redirectURIs, postLogoutURIs)
}

func (c *Client) projectIDByName(ctx context.Context, tenantBase, orgID, name string) (string, error) {
	var out struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	err := c.do(ctx, http.MethodPost, tenantBase+"/management/v1/projects/_search", orgID, map[string]any{
		"queries": []any{map[string]any{"nameQuery": map[string]any{"name": name}}},
	}, &out)
	if err != nil {
		return "", err
	}
	if len(out.Result) == 0 {
		return "", ErrNotFound
	}
	return out.Result[0].ID, nil
}

type oidcApp struct {
	ID         string `json:"id"`
	OIDCConfig struct {
		ClientID               string   `json:"clientId"`
		RedirectURIs           []string `json:"redirectUris"`
		PostLogoutRedirectURIs []string `json:"postLogoutRedirectUris"`
	} `json:"oidcConfig"`
}

func (c *Client) oidcAppByName(ctx context.Context, base, orgID, projectID, name string) (*oidcApp, error) {
	var out struct {
		Result []oidcApp `json:"result"`
	}
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/_search", base, projectID), orgID,
		map[string]any{
			"queries": []any{map[string]any{"nameQuery": map[string]any{"name": name}}},
		}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Result) == 0 || out.Result[0].OIDCConfig.ClientID == "" {
		return nil, ErrNotFound
	}
	return &out.Result[0], nil
}

// union returns base plus whatever from extra is missing, reporting
// whether anything was added.
func union(base, extra []string) ([]string, bool) {
	seen := map[string]bool{}
	for _, v := range base {
		seen[v] = true
	}
	added := false
	for _, v := range extra {
		if !seen[v] {
			base, seen[v], added = append(base, v), true, true
		}
	}
	return base, added
}

// EnsureHumanUser creates a human user with a password if no user with
// that username exists yet (management v1 import — the endpoint that
// accepts a password without an init flow).
func (c *Client) EnsureHumanUser(ctx context.Context, tenantBase, orgID, username, email, password string) error {
	var found struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	err := c.do(ctx, http.MethodPost, tenantBase+"/management/v1/users/_search", orgID, map[string]any{
		"queries": []any{map[string]any{"userNameQuery": map[string]any{"userName": username}}},
	}, &found)
	if err != nil {
		return err
	}
	if len(found.Result) > 0 {
		return nil
	}
	return c.do(ctx, http.MethodPost, tenantBase+"/management/v1/users/human/_import", orgID, map[string]any{
		"userName": username,
		"profile":  map[string]any{"firstName": "Initial", "lastName": "Admin"},
		"email":    map[string]any{"email": email, "isEmailVerified": true},
		"password": password,
		// The credential is a generated handover secret, not a human
		// choice — forcing a change on first login is the product-correct
		// follow-up once account recovery exists (M3+).
		"passwordChangeRequired": false,
	}, nil)
}

// EnsureMachineUser creates (or finds) a machine user — the automation
// identity for operators' scripts and CI. Returns the user ID.
func (c *Client) EnsureMachineUser(ctx context.Context, base, orgID, username string) (string, error) {
	var found struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	err := c.do(ctx, http.MethodPost, base+"/management/v1/users/_search", orgID, map[string]any{
		"queries": []any{map[string]any{"userNameQuery": map[string]any{"userName": username}}},
	}, &found)
	if err != nil {
		return "", err
	}
	if len(found.Result) > 0 {
		return found.Result[0].ID, nil
	}
	var out struct {
		UserID string `json:"userId"`
	}
	err = c.do(ctx, http.MethodPost, base+"/management/v1/users/machine", orgID, map[string]any{
		"userName":        username,
		"name":            username,
		"accessTokenType": "ACCESS_TOKEN_TYPE_BEARER",
	}, &out)
	return out.UserID, err
}

// CreatePAT issues a personal access token for a machine user.
func (c *Client) CreatePAT(ctx context.Context, base, orgID, userID string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/users/%s/pats", base, userID), orgID,
		map[string]any{"expirationDate": "2028-01-01T00:00:00Z"}, &out)
	return out.Token, err
}

// AddMachineKey creates a JSON (JWT-profile) key for a machine user and
// returns the raw service-account key JSON — {"type","keyId","key" (PEM
// private key),"userId"}. Zitadel returns the private key only once, at
// creation, so the caller must persist it. This is the credential a service
// uses to authenticate to the token endpoint (client assertion) for the
// jwt-bearer and token-exchange grants (ADR-0017).
func (c *Client) AddMachineKey(ctx context.Context, base, orgID, userID string) ([]byte, error) {
	var out struct {
		KeyDetails string `json:"keyDetails"` // base64 of the service-account JSON
	}
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/users/%s/keys", base, userID), orgID,
		map[string]any{"type": "KEY_TYPE_JSON", "expirationDate": "2028-01-01T00:00:00Z"}, &out)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.KeyDetails)
}

// ProjectID returns the org's "peristera" app project id, creating it if
// absent. Apps need it to scope token-exchange audiences (ADR-0017).
func (c *Client) ProjectID(ctx context.Context, base, orgID string) (string, error) {
	projectID, err := c.projectIDByName(ctx, base, orgID, "peristera")
	if errors.Is(err, ErrNotFound) {
		var out struct {
			ID string `json:"id"`
		}
		err = c.do(ctx, http.MethodPost, base+"/management/v1/projects", orgID,
			map[string]any{"name": "peristera"}, &out)
		projectID = out.ID
	}
	return projectID, err
}

// EnsureS2SClient makes sure a confidential OIDC app for service-to-service
// token exchange exists (ADR-0017) and returns its client id and app id (the
// app id is needed to attach a key). The app has the token-exchange grant,
// private_key_jwt auth (no shared secret), and JWT access tokens. Idempotent.
func (c *Client) EnsureS2SClient(ctx context.Context, base, orgID, name string) (clientID, appID string, err error) {
	projectID, err := c.ProjectID(ctx, base, orgID)
	if err != nil {
		return "", "", err
	}
	if app, err := c.oidcAppByName(ctx, base, orgID, projectID, name); err == nil {
		return app.OIDCConfig.ClientID, app.ID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", "", err
	}
	var out struct {
		AppID    string `json:"appId"`
		ClientID string `json:"clientId"`
	}
	err = c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/oidc", base, projectID), orgID,
		map[string]any{
			"name": name,
			// A redirect + response type is required for OIDC apps; this
			// client never runs the auth-code flow (it only exchanges), but
			// the fields must be present.
			"redirectUris":    []string{"http://localhost/unused"},
			"responseTypes":   []string{"OIDC_RESPONSE_TYPE_CODE"},
			"grantTypes":      []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_TOKEN_EXCHANGE"},
			"appType":         "OIDC_APP_TYPE_WEB",
			"authMethodType":  "OIDC_AUTH_METHOD_TYPE_PRIVATE_KEY_JWT",
			"accessTokenType": "OIDC_TOKEN_TYPE_JWT",
			"devMode":         true,
		}, &out)
	return out.ClientID, out.AppID, err
}

// AddAppKey generates a JSON (private_key_jwt) key for an OIDC app and returns
// the raw key JSON — {"keyId","key" (PEM),"clientId"}. Returned only once, at
// creation; the caller must persist it. lib/svcauth signs client assertions
// with it (ADR-0017).
func (c *Client) AddAppKey(ctx context.Context, base, orgID, appID string) ([]byte, error) {
	projectID, err := c.ProjectID(ctx, base, orgID)
	if err != nil {
		return nil, err
	}
	var out struct {
		KeyDetails string `json:"keyDetails"`
	}
	err = c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/%s/keys", base, projectID, appID), orgID,
		map[string]any{"type": "KEY_TYPE_JSON", "expirationDate": "2028-01-01T00:00:00Z"}, &out)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.KeyDetails)
}

// EnableImpersonation turns on the instance security setting that permits
// token-exchange delegation (actor_token). Idempotent — a repeat PUT that
// changes nothing returns Zitadel's "No changes", which we treat as success.
func (c *Client) EnableImpersonation(ctx context.Context, base string) error {
	err := c.do(ctx, http.MethodPut, base+"/admin/v1/policies/security", "",
		map[string]any{"enableImpersonation": true}, nil)
	if err != nil && strings.Contains(err.Error(), "No changes") {
		return nil
	}
	return err
}

// UserinfoOK reports whether a bearer token is accepted by the issuer's
// userinfo endpoint — the control plane's cheap token validation.
func (c *Client) UserinfoOK(ctx context.Context, issuer, token string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/oidc/v1/userinfo", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// EnsureWebApp makes sure a public PKCE OIDC app with this name exists in
// the org's "peristera" project and returns its client ID (same shape as
// EnsureStubApp, for arbitrary app names).
func (c *Client) EnsureWebApp(ctx context.Context, base, orgID, name string, redirectURIs, postLogoutURIs []string) (string, error) {
	projectID, err := c.projectIDByName(ctx, base, orgID, "peristera")
	if errors.Is(err, ErrNotFound) {
		var out struct {
			ID string `json:"id"`
		}
		err = c.do(ctx, http.MethodPost, base+"/management/v1/projects", orgID,
			map[string]any{"name": "peristera"}, &out)
		projectID = out.ID
	}
	if err != nil {
		return "", err
	}
	if app, err := c.oidcAppByName(ctx, base, orgID, projectID, name); err == nil {
		// Reconcile redirect URIs: the same logical app may serve several
		// public URLs over time (localhost dev, in-cluster ingress).
		redirects, addR := union(app.OIDCConfig.RedirectURIs, redirectURIs)
		logouts, addL := union(app.OIDCConfig.PostLogoutRedirectURIs, postLogoutURIs)
		if addR || addL {
			err := c.do(ctx, http.MethodPut,
				fmt.Sprintf("%s/management/v1/projects/%s/apps/%s/oidc_config", base, projectID, app.ID), orgID,
				map[string]any{
					"redirectUris":             redirects,
					"postLogoutRedirectUris":   logouts,
					"responseTypes":            []string{"OIDC_RESPONSE_TYPE_CODE"},
					"grantTypes":               []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
					"appType":                  "OIDC_APP_TYPE_WEB",
					"authMethodType":           "OIDC_AUTH_METHOD_TYPE_NONE",
					"accessTokenType":          "OIDC_TOKEN_TYPE_BEARER",
					"devMode":                  true,
					"idTokenUserinfoAssertion": true,
				}, nil)
			if err != nil {
				return "", err
			}
		}
		return app.OIDCConfig.ClientID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	var out struct {
		ClientID string `json:"clientId"`
	}
	err = c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/oidc", base, projectID), orgID,
		map[string]any{
			"name":                     name,
			"redirectUris":             redirectURIs,
			"postLogoutRedirectUris":   postLogoutURIs,
			"responseTypes":            []string{"OIDC_RESPONSE_TYPE_CODE"},
			"grantTypes":               []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
			"appType":                  "OIDC_APP_TYPE_WEB",
			"authMethodType":           "OIDC_AUTH_METHOD_TYPE_NONE",
			"accessTokenType":          "OIDC_TOKEN_TYPE_BEARER",
			"devMode":                  true,
			"idTokenUserinfoAssertion": true,
		}, &out)
	return out.ClientID, err
}

// DiscoveryAlive reports whether the instance behind issuer serves OIDC
// discovery yet (a fresh instance lags for a few seconds).
func (c *Client) DiscoveryAlive(ctx context.Context, issuer string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
