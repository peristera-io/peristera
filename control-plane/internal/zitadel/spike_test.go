package zitadel

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
	"os"
	"strings"
	"testing"
	"time"
)

// TestSpikeTokenExchange is a manual, live spike (gated on ZITADEL_SPIKE=1)
// that verifies the RFC 8693 delegation path against a running tenant
// instance before we wire provisioning (M5 S2, ADR-0017):
//
//	machine user + JSON key  ->  jwt-bearer grant (machine access token)
//	                         ->  token-exchange, plain (subject only)
//	                         ->  token-exchange with actor_token (delegation)
//
// Env: SYSTEM_USER_KEY, ZITADEL_BASE_URL (deployment issuer),
// SPIKE_TENANT_BASE (e.g. http://s2.127.0.0.1.sslip.io:9080).
func TestSpikeTokenExchange(t *testing.T) {
	if os.Getenv("ZITADEL_SPIKE") != "1" {
		t.Skip("set ZITADEL_SPIKE=1 to run the live token-exchange spike")
	}
	ctx := context.Background()
	base := os.Getenv("SPIKE_TENANT_BASE")
	c, err := NewFromKeyFile(os.Getenv("ZITADEL_BASE_URL"), env("SYSTEM_USER_ID", "admin-client"), os.Getenv("SYSTEM_USER_KEY"))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	orgID, err := c.FirstOrgID(ctx, base)
	if err != nil {
		t.Fatalf("orgID: %v", err)
	}

	// Enable token exchange + impersonation at the INSTANCE level. Client
	// auth is valid (client_credentials works) but the exchange grant is
	// rejected until these are on. Log outcomes; don't fail the test.
	var featRaw json.RawMessage
	errFeat := c.do(ctx, http.MethodPut, base+"/v2/features/instance", "",
		map[string]any{"oidcTokenExchange": true}, &featRaw)
	t.Logf("feature oidcTokenExchange: err=%v resp=%s", errFeat, featRaw)
	errSec := c.do(ctx, http.MethodPut, base+"/admin/v1/policies/security", "",
		map[string]any{"enableImpersonation": true}, nil)
	t.Logf("security enableImpersonation: err=%v", errSec)
	time.Sleep(4 * time.Second)

	uid, err := c.EnsureMachineUser(ctx, base, orgID, "svc-spike")
	if err != nil {
		t.Fatalf("machine user: %v", err)
	}
	keyJSON, err := c.AddMachineKey(ctx, base, orgID, uid)
	if err != nil {
		t.Fatalf("machine key: %v", err)
	}
	var sa struct {
		KeyID  string `json:"keyId"`
		Key    string `json:"key"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(keyJSON, &sa); err != nil {
		t.Fatalf("key json: %v", err)
	}
	priv := parseRSAPEM(t, sa.Key)
	t.Logf("machine user %s, key %s", sa.UserID, sa.KeyID)

	// (1) jwt-bearer grant: the machine authenticates with a client assertion
	// and gets its own access token.
	assertion := signAssertion(t, priv, sa.KeyID, sa.UserID, base)
	machineTok, body := postToken(t, base, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
		"scope":      {"openid profile"},
	})
	if machineTok == "" {
		t.Fatalf("jwt-bearer grant returned no access_token: %s", body)
	}
	t.Logf("jwt-bearer OK: got machine access token (%d chars)", len(machineTok))

	// (2) token exchange, plain (subject only) — authenticate the request
	// with the machine's own access token (Bearer), not a client assertion.
	_, plainBody := postTokenAuth(t, base, machineTok, url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {machineTok},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
	})
	t.Logf("EXCHANGE (plain, bearer-auth): %s", plainBody)

	// (3) token exchange with actor_token (delegation) — the gated path.
	_, actorBody := postTokenAuth(t, base, machineTok, url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {machineTok},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
		"actor_token":          {machineTok},
		"actor_token_type":     {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
	})
	t.Logf("EXCHANGE (actor_token/delegation, bearer-auth): %s", actorBody)

	// (4) same, but authenticate with a client assertion whose aud is the
	// token endpoint (some IdPs require the endpoint, not the issuer).
	_, tep := postToken(t, base, url.Values{
		"grant_type":            {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":         {machineTok},
		"subject_token_type":    {"urn:ietf:params:oauth:token-type:access_token"},
		"actor_token":           {machineTok},
		"actor_token_type":      {"urn:ietf:params:oauth:token-type:access_token"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {signAssertion(t, priv, sa.KeyID, sa.UserID, base+"/oauth/v2/token")},
	})
	t.Logf("EXCHANGE (actor, client_assertion aud=token-endpoint): %s", tep)

	// (5) The right model: authenticate the exchange as an API *application*
	// (a real OIDC client), not the machine user. Create an API app with
	// private-key-jwt auth, generate an app key, and use ITS clientId in the
	// client assertion.
	projID, err := c.projectIDByName(ctx, base, orgID, "peristera")
	if err != nil {
		t.Fatalf("projectID: %v", err)
	}
	var app struct {
		AppID    string `json:"appId"`
		ClientID string `json:"clientId"`
	}
	appName := fmt.Sprintf("svc-spike-api-%d", time.Now().Unix())
	var appBasic struct {
		AppID        string `json:"appId"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/api", base, projID), orgID,
		map[string]any{"name": appName + "-basic", "authMethodType": "API_AUTH_METHOD_TYPE_BASIC"}, &appBasic); err != nil {
		t.Fatalf("create basic api app: %v", err)
	}
	// The OIDC client_id is "<id>@<project>", not the bare numeric id the
	// create response returns — that is what caused "no active client".
	clientID := appBasic.ClientID
	if !strings.Contains(clientID, "@") {
		clientID += "@peristera"
	}
	t.Logf("BASIC api app clientId=%s hasSecret=%v", clientID, appBasic.ClientSecret != "")
	basicHdr := "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+appBasic.ClientSecret))
	var tokB, bodyB string
	for i := 0; i < 6; i++ {
		time.Sleep(2 * time.Second)
		// PLAIN exchange (no actor_token) to isolate client resolution from
		// the impersonation gate.
		tokB, bodyB = postTokenHeader(t, base, basicHdr, url.Values{
			"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
			"subject_token":      {machineTok},
			"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		})
		if tokB != "" || !strings.Contains(bodyB, "no active client") {
			break
		}
	}
	t.Logf("EXCHANGE (BASIC @project client, PLAIN no-actor): got_token=%v :: %s", tokB != "", bodyB)

	// And with actor_token (delegation) — expected to need the instance
	// impersonation setting + an impersonator role on the actor.
	_, bodyD := postTokenHeader(t, base, basicHdr, url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {machineTok},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"actor_token":        {machineTok},
		"actor_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
	})
	t.Logf("EXCHANGE (BASIC @project client, DELEGATION actor): %s", bodyD)

	if err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/api", base, projID), orgID,
		map[string]any{"name": appName, "authMethodType": "API_AUTH_METHOD_TYPE_PRIVATE_KEY_JWT"}, &app); err != nil {
		t.Fatalf("create api app: %v", err)
	}
	var appKey struct {
		KeyDetails string `json:"keyDetails"`
	}
	if err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/%s/keys", base, projID, app.AppID), orgID,
		map[string]any{"type": "KEY_TYPE_JSON", "expirationDate": "2028-01-01T00:00:00Z"}, &appKey); err != nil {
		t.Fatalf("create app key: %v", err)
	}
	rawKey, _ := base64.StdEncoding.DecodeString(appKey.KeyDetails)
	var ak struct {
		KeyID    string `json:"keyId"`
		Key      string `json:"key"`
		ClientID string `json:"clientId"`
	}
	json.Unmarshal(rawKey, &ak)
	t.Logf("API app clientId=%s keyId=%s", ak.ClientID, ak.KeyID)
	appPriv := parseRSAPEM(t, ak.Key)

	// Zitadel projects new clients/keys asynchronously — a just-created app
	// can be "no active client" for a few seconds. Retry.
	var tok5, body5 string
	for i := 0; i < 8; i++ {
		time.Sleep(2 * time.Second)
		tok5, body5 = postToken(t, base, url.Values{
			"grant_type":            {"urn:ietf:params:oauth:grant-type:token-exchange"},
			"subject_token":         {machineTok},
			"subject_token_type":    {"urn:ietf:params:oauth:token-type:access_token"},
			"actor_token":           {machineTok},
			"actor_token_type":      {"urn:ietf:params:oauth:token-type:access_token"},
			"requested_token_type":  {"urn:ietf:params:oauth:token-type:access_token"},
			"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
			"client_assertion":      {signAssertion(t, appPriv, ak.KeyID, ak.ClientID, base)},
		})
		if tok5 != "" || !strings.Contains(body5, "no active client") {
			break
		}
	}
	t.Logf("EXCHANGE (API-app client, delegation): got_token=%v :: %s", tok5 != "", body5)

	// (6) The v2-hint path: give the *machine user* a client secret. The
	// secret endpoint returns the AUTHORITATIVE clientId — no guessing the
	// "<id>@project" format. Then authenticate the exchange as that client.
	var msec struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := c.do(ctx, http.MethodPut,
		fmt.Sprintf("%s/management/v1/users/%s/secret", base, uid), orgID, map[string]any{}, &msec); err != nil {
		t.Fatalf("machine secret: %v", err)
	}
	t.Logf("machine-user secret: clientId=%s hasSecret=%v", msec.ClientID, msec.ClientSecret != "")
	muHdr := "Basic " + base64.StdEncoding.EncodeToString([]byte(msec.ClientID+":"+msec.ClientSecret))

	// Isolation: does this client work AT ALL via client_credentials? If yes,
	// the client is valid and token-exchange rejects for a grant/config
	// reason; if no, the client auth itself (id/secret transport) is broken.
	ccTok, ccBody := postTokenHeader(t, base, muHdr, url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"openid"},
	})
	t.Logf("CLIENT_CREDENTIALS (machine-user secret): got_token=%v :: %s", ccTok != "", ccBody)

	// (7) THE hypothesis: an OIDC app whose grant types INCLUDE token-exchange,
	// with a client secret. "no active client" is Zitadel's error for a client
	// that exists but lacks the token-exchange grant.
	var oapp struct {
		AppID        string `json:"appId"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/management/v1/projects/%s/apps/oidc", base, projID), orgID, map[string]any{
			"name":           fmt.Sprintf("svc-spike-tex-%d", time.Now().Unix()),
			"redirectUris":   []string{"http://localhost/cb"},
			"responseTypes":  []string{"OIDC_RESPONSE_TYPE_CODE"},
			"grantTypes":     []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_TOKEN_EXCHANGE"},
			"appType":        "OIDC_APP_TYPE_WEB",
			"authMethodType": "OIDC_AUTH_METHOD_TYPE_BASIC",
			"accessTokenType": "OIDC_TOKEN_TYPE_JWT",
		}, &oapp); err != nil {
		t.Fatalf("create oidc tex app: %v", err)
	}
	t.Logf("OIDC tex app clientId=%s hasSecret=%v", oapp.ClientID, oapp.ClientSecret != "")
	texHdr := "Basic " + base64.StdEncoding.EncodeToString([]byte(oapp.ClientID+":"+oapp.ClientSecret))
	time.Sleep(3 * time.Second) // let the app project

	// Mint a FRESH subject token whose audience includes the project (so the
	// exchanging client is a valid audience) — the "subject_token invalid"
	// fix.
	projAud := fmt.Sprintf("urn:zitadel:iam:org:project:id:%s:aud", projID)
	subjTok, subjBody := postTokenHeader(t, base, muHdr, url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"openid " + projAud},
	})
	t.Logf("subject token (project-aud): got=%v :: %s", subjTok != "", firstN(subjBody, 120))
	if subjTok == "" {
		subjTok = machineTok
	}

	var tok7, body7 string
	for i := 0; i < 6; i++ {
		time.Sleep(2 * time.Second)
		tok7, body7 = postTokenHeader(t, base, texHdr, url.Values{
			"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
			"subject_token":      {subjTok},
			"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
			"scope":              {"openid " + projAud},
		})
		if tok7 != "" || !strings.Contains(body7, "no active client") {
			break
		}
	}
	t.Logf("EXCHANGE (OIDC tex app, project-aud subject, PLAIN): got_token=%v :: %s", tok7 != "", body7)
	var tok6, body6 string
	for i := 0; i < 6; i++ {
		time.Sleep(2 * time.Second)
		tok6, body6 = postTokenHeader(t, base, muHdr, url.Values{
			"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
			"subject_token":      {machineTok},
			"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		})
		if tok6 != "" || !strings.Contains(body6, "no active client") {
			break
		}
	}
	t.Logf("EXCHANGE (machine-user secret client, PLAIN): got_token=%v :: %s", tok6 != "", body6)
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func parseRSAPEM(t *testing.T, pemStr string) *rsa.PrivateKey {
	t.Helper()
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		t.Fatalf("no PEM block in machine key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	return parsed.(*rsa.PrivateKey)
}

func signAssertion(t *testing.T, priv *rsa.PrivateKey, kid, userID, issuer string) string {
	t.Helper()
	b64 := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now()
	header := b64(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	claims := b64(map[string]any{
		"iss": userID, "sub": userID, "aud": issuer,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	})
	signingInput := header + "." + claims
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// postToken posts to the tenant token endpoint and returns (access_token, rawBody).
func postToken(t *testing.T, base string, form url.Values) (string, string) {
	return postTokenAuth(t, base, "", form)
}

// postTokenAuth posts to the token endpoint with an optional Bearer token.
func postTokenAuth(t *testing.T, base, bearer string, form url.Values) (string, string) {
	auth := ""
	if bearer != "" {
		auth = "Bearer " + bearer
	}
	return postTokenHeader(t, base, auth, form)
}

// postTokenHeader posts to the token endpoint with a raw Authorization header.
func postTokenHeader(t *testing.T, base, authHeader string, form url.Values) (string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/oauth/v2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
	}
	json.Unmarshal(raw, &out)
	return out.AccessToken, fmt.Sprintf("HTTP %d %s", resp.StatusCode, string(raw))
}
