// Package svcauth is the Peristera service-to-service auth convention
// (ADR-0017): a service calls another *on behalf of a logged-in user* via
// OAuth2 token exchange (RFC 8693). The caller authenticates as its own
// per-app S2S OIDC client using private_key_jwt (a client assertion signed
// with the app's private key — no shared secret at rest) and exchanges the
// user's access token for one carrying the user as subject and the calling
// service as the authorized party (azp), scoped to the tenant's app project.
//
// This package is the *client* half (minting the on-behalf-of token). The
// callee half (validating it) is added in M5 s3.
package svcauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProjectAudienceScope is the Zitadel scope that puts the tenant's app
// project into a token's audience. lib/oidcrp must request it at login so the
// user's access token is exchangeable here; OnBehalfOf requests it on the
// exchanged token too. Kept in one place so login and exchange stay in sync.
func ProjectAudienceScope(projectID string) string {
	return "urn:zitadel:iam:org:project:id:" + projectID + ":aud"
}

// appKey is the JSON Zitadel issues for an application key (KEY_TYPE_JSON):
// the private-key credential of the per-app S2S OIDC client.
type appKey struct {
	KeyID    string `json:"keyId"`
	Key      string `json:"key"` // PEM (PKCS#1 or PKCS#8) RSA private key
	ClientID string `json:"clientId"`
}

// Exchanger performs on-behalf-of token exchange for one app in one tenant.
type Exchanger struct {
	issuer    string
	tokenURL  string
	clientID  string
	keyID     string
	key       *rsa.PrivateKey
	projScope string // urn:zitadel:iam:org:project:id:<projectID>:aud
	http      *http.Client
}

// NewExchanger builds an Exchanger from the app-key JSON (the per-app S2S
// client credential the control plane provisions into a Secret), the tenant
// OIDC issuer, and the tenant's app project id. The project id scopes the
// exchanged token's audience to the tenant's apps.
func NewExchanger(issuer, projectID string, keyJSON []byte) (*Exchanger, error) {
	var k appKey
	if err := json.Unmarshal(keyJSON, &k); err != nil {
		return nil, fmt.Errorf("svcauth: parsing app key: %w", err)
	}
	if k.ClientID == "" || k.Key == "" {
		return nil, fmt.Errorf("svcauth: app key missing clientId or key")
	}
	priv, err := parseRSAKey(k.Key)
	if err != nil {
		return nil, fmt.Errorf("svcauth: %w", err)
	}
	return &Exchanger{
		issuer:    issuer,
		tokenURL:  strings.TrimRight(issuer, "/") + "/oauth/v2/token",
		clientID:  k.ClientID,
		keyID:     k.KeyID,
		key:       priv,
		projScope: ProjectAudienceScope(projectID),
		http:      &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// ProjectScope is the Zitadel scope that makes a token's audience include the
// tenant's app project — the browser login (lib/oidcrp) must request it so
// the user's access token is exchangeable here. Exposed so callers stay in
// sync with the exact scope string.
func (e *Exchanger) ProjectScope() string { return e.projScope }

// OnBehalfOf exchanges a logged-in user's access token for one the calling
// service can present to another Peristera service in the same tenant. The
// returned token carries the user as `sub` and this service's client as
// `azp`, scoped to the tenant's app project.
func (e *Exchanger) OnBehalfOf(ctx context.Context, userAccessToken string) (string, error) {
	form := url.Values{
		"grant_type":            {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":         {userAccessToken},
		"subject_token_type":    {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type":  {"urn:ietf:params:oauth:token-type:access_token"},
		"scope":                 {"openid " + e.projScope},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {e.assertion()},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("svcauth: token exchange: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("svcauth: token exchange: %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("svcauth: token exchange response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("svcauth: token exchange returned no access_token")
	}
	return out.AccessToken, nil
}

// assertion mints a short-lived private_key_jwt client assertion authenticating
// this app's S2S OIDC client to the token endpoint.
func (e *Exchanger) assertion() string {
	now := time.Now()
	header := b64json(map[string]any{"alg": "RS256", "typ": "JWT", "kid": e.keyID})
	claims := b64json(map[string]any{
		"iss": e.clientID,
		"sub": e.clientID,
		"aud": e.issuer,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	})
	signingInput := header + "." + claims
	sum := sha256.Sum256([]byte(signingInput))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, e.key, crypto.SHA256, sum[:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func b64json(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseRSAKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block in app key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing app key: %w", err)
	}
	k, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("app key is not RSA")
	}
	return k, nil
}
