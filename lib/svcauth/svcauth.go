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
//
// Callee contract: the returned token's audience is the whole tenant app
// **project**, not a single callee — a token minted to call Kamara is also
// audience-valid at Ergonomos/stub. So a callee must NOT treat "audience
// matches" as "this token was meant for me". It authorizes on the **user**
// (`sub`) via OpenFGA and relies on the network layer (ADR-0016) to bound
// which services can reach it; the audience only proves "some app in this
// tenant". The callee-side validation (M5 s3) enforces this.
func (e *Exchanger) OnBehalfOf(ctx context.Context, userAccessToken string) (string, error) {
	if userAccessToken == "" {
		return "", fmt.Errorf("svcauth: no user access token to exchange")
	}
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
	return clientAssertion(e.clientID, e.keyID, e.issuer, e.key)
}

// clientAssertion mints a private_key_jwt client assertion (iss=sub=clientID,
// aud=issuer) — the auth both the exchange (Exchanger) and introspection
// (Validator) use.
func clientAssertion(clientID, keyID, issuer string, key *rsa.PrivateKey) string {
	now := time.Now()
	header := b64json(map[string]any{"alg": "RS256", "typ": "JWT", "kid": keyID})
	claims := b64json(map[string]any{
		"iss": clientID,
		"sub": clientID,
		"aud": issuer,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	})
	signingInput := header + "." + claims
	sum := sha256.Sum256([]byte(signingInput))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Validator is the callee half (ADR-0017): it validates an incoming token via
// introspection, authenticating as the callee's own S2S client
// (private_key_jwt). Introspection (not local JWT) because the exchanged
// token is opaque and because it also lets the callee check live revocation
// and recover the calling service for the audit actor.
type Validator struct {
	introspectURL string
	clientID      string
	keyID         string
	key           *rsa.PrivateKey
	issuer        string
	http          *http.Client
}

// TokenInfo is what a callee learns about a presented token.
type TokenInfo struct {
	Active bool
	// Subject is the acting user (the OIDC sub).
	Subject string
	// ActorClient is the azp — the OIDC client that obtained the token. For
	// an on-behalf-of call it is the calling service's S2S client id.
	ActorClient string
	// ActorName is that client's human name if introspection exposes it
	// (e.g. "ergonomos-s2s") — the audit actor.
	ActorName string
}

// NewValidator builds a Validator from the callee's own app-key JSON.
func NewValidator(issuer string, keyJSON []byte) (*Validator, error) {
	var k appKey
	if err := json.Unmarshal(keyJSON, &k); err != nil {
		return nil, fmt.Errorf("svcauth: parsing app key: %w", err)
	}
	priv, err := parseRSAKey(k.Key)
	if err != nil {
		return nil, fmt.Errorf("svcauth: %w", err)
	}
	return &Validator{
		introspectURL: strings.TrimRight(issuer, "/") + "/oauth/v2/introspect",
		clientID:      k.ClientID,
		keyID:         k.KeyID,
		key:           priv,
		issuer:        issuer,
		http:          &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Introspect validates token at the IdP and returns what it reveals.
func (v *Validator) Introspect(ctx context.Context, token string) (TokenInfo, error) {
	form := url.Values{
		"token":                 {token},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {clientAssertion(v.clientID, v.keyID, v.issuer, v.key)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.introspectURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenInfo{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.http.Do(req)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("svcauth: introspect: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return TokenInfo{}, fmt.Errorf("svcauth: introspect: %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Active   bool   `json:"active"`
		Sub      string `json:"sub"`
		Azp      string `json:"azp"`
		Username string `json:"username"`
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return TokenInfo{}, fmt.Errorf("svcauth: introspect response: %w", err)
	}
	actorClient := out.Azp
	if actorClient == "" {
		actorClient = out.ClientID
	}
	return TokenInfo{Active: out.Active, Subject: out.Sub, ActorClient: actorClient, ActorName: out.Username}, nil
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
