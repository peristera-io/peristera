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
		return fmt.Errorf("%s %s: %d: %s", method, url, resp.StatusCode, raw)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
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
// the deployment's ExternalDomain) serve this instance. Idempotent:
// "already exists" answers are swallowed.
func (c *Client) AddTrustedDomain(ctx context.Context, tenantBase, instanceID, domain string) error {
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/v2beta/instances/%s/trusted-domains", tenantBase, instanceID), "",
		map[string]any{"domain": domain}, nil)
	if err != nil && bytes.Contains([]byte(err.Error()), []byte("already exists")) {
		return nil
	}
	return err
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
	projectID, err := c.projectIDByName(ctx, tenantBase, orgID, "peristera")
	if errors.Is(err, ErrNotFound) {
		var out struct {
			ID string `json:"id"`
		}
		err = c.do(ctx, http.MethodPost, tenantBase+"/management/v1/projects", orgID,
			map[string]any{"name": "peristera"}, &out)
		projectID = out.ID
	}
	if err != nil {
		return "", err
	}

	if clientID, err := c.oidcClientIDByName(ctx, tenantBase, orgID, projectID, "stub"); err == nil {
		return clientID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}

	var out struct {
		ClientID string `json:"clientId"`
	}
	err = c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/oidc", tenantBase, projectID), orgID,
		map[string]any{
			"name":                     "stub",
			"redirectUris":             redirectURIs,
			"postLogoutRedirectUris":   postLogoutURIs,
			"responseTypes":            []string{"OIDC_RESPONSE_TYPE_CODE"},
			"grantTypes":               []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
			"appType":                  "OIDC_APP_TYPE_WEB",
			"authMethodType":           "OIDC_AUTH_METHOD_TYPE_NONE",
			"accessTokenType":          "OIDC_TOKEN_TYPE_BEARER",
			"devMode":                  true, // dev: http redirect URIs
			"idTokenUserinfoAssertion": true, // else name/email claims are empty
		}, &out)
	return out.ClientID, err
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

func (c *Client) oidcClientIDByName(ctx context.Context, tenantBase, orgID, projectID, name string) (string, error) {
	var out struct {
		Result []struct {
			OIDCConfig struct {
				ClientID string `json:"clientId"`
			} `json:"oidcConfig"`
		} `json:"result"`
	}
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/_search", tenantBase, projectID), orgID,
		map[string]any{
			"queries": []any{map[string]any{"nameQuery": map[string]any{"name": name}}},
		}, &out)
	if err != nil {
		return "", err
	}
	if len(out.Result) == 0 || out.Result[0].OIDCConfig.ClientID == "" {
		return "", ErrNotFound
	}
	return out.Result[0].OIDCConfig.ClientID, nil
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
