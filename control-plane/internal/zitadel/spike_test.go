package zitadel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/peristera-io/peristera/lib/svcauth"
)

// TestS2SExchangeLive is a manual, live integration test (gated on
// ZITADEL_SPIKE=1) exercising the REAL S2S provisioning + exchange path
// against a running tenant instance (M5 s2, ADR-0017):
//
//	EnsureS2SClient + AddAppKey  ->  svcauth.Exchanger.OnBehalfOf
//
// A machine user with a project-audience token stands in for a logged-in
// user's access token. Env: SYSTEM_USER_KEY, ZITADEL_BASE_URL,
// SPIKE_TENANT_BASE (e.g. http://s2d.127.0.0.1.sslip.io:9080).
func TestS2SExchangeLive(t *testing.T) {
	if os.Getenv("ZITADEL_SPIKE") != "1" {
		t.Skip("set ZITADEL_SPIKE=1 to run the live S2S exchange test")
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
	projID, err := c.ProjectID(ctx, base, orgID)
	if err != nil {
		t.Fatalf("projectID: %v", err)
	}
	// Deliberately NOT enabling impersonation — verifying the plain exchange
	// works without it (s2 review #4).

	// Provision the caller's S2S client + key (the real methods).
	_, appID, err := c.EnsureS2SClient(ctx, base, orgID, "ergonomos-s2s")
	if err != nil {
		t.Fatalf("EnsureS2SClient: %v", err)
	}
	keyJSON, err := c.AddAppKey(ctx, base, orgID, appID)
	if err != nil {
		t.Fatalf("AddAppKey: %v", err)
	}

	// Stand-in for a logged-in user's access token: a machine user whose
	// token carries the project audience (as lib/oidcrp will request).
	subjTok, subjUID := projectAudToken(t, c, ctx, base, orgID, projID)

	// The real client-side exchange.
	ex, err := svcauth.NewExchanger(base, projID, keyJSON)
	if err != nil {
		t.Fatalf("NewExchanger: %v", err)
	}
	exchanged, err := ex.OnBehalfOf(ctx, subjTok)
	if err != nil {
		t.Fatalf("OnBehalfOf: %v", err)
	}
	if exchanged == "" {
		t.Fatal("OnBehalfOf returned empty token")
	}
	// Token format (JWT vs Zitadel's opaque JWE): the callee's local-vs-
	// introspection decision hinges on this.
	hdr := exchanged
	if i := strings.IndexByte(hdr, '.'); i > 0 {
		if b, err := base64.RawURLEncoding.DecodeString(hdr[:i]); err == nil {
			hdr = string(b)
		}
	}
	t.Logf("exchanged token header: %s", firstN(hdr, 80))

	// The critical acceptance check: does the exchanged token resolve to the
	// USER (sub) at the callee's existing userinfo path? If so, Kamara owns
	// the file to the user with no callee change.
	sub := userinfoSub(t, ctx, base, exchanged)
	t.Logf("userinfo(sub)=%s  expected user=%s  match=%v", sub, subjUID, sub == subjUID)
	if sub != subjUID {
		t.Fatalf("exchanged token resolves to %q, not the user %q", sub, subjUID)
	}
	t.Logf("OK — exchanged token resolves to the user via userinfo")

	// ACCEPTANCE (R57): upload to the tenant's live Kamara on behalf of the
	// user (exactly what ergonomos's kamara.Client does), then confirm the
	// USER owns the file — i.e. sees it with their own token.
	kamaraURL := strings.Replace(base, "://", "://kamara.", 1)
	fileID := kamaraUpload(t, ctx, kamaraURL, exchanged, "acceptance.txt", "on-behalf-of hello")
	t.Logf("uploaded to Kamara on behalf of the user: file %s", fileID)
	if !kamaraUserOwns(t, ctx, kamaraURL, subjTok, fileID) {
		t.Fatalf("file %s not visible to the user's own token — not owned by them", fileID)
	}
	t.Logf("ACCEPTANCE OK — on-behalf-of upload; Kamara owns the file to the user")

	// Can the callee recover the calling SERVICE (for the audit actor) via
	// svcauth.Validator introspection? (Uses ergonomos's key as the
	// introspecting client — any confidential client works.)
	val, err := svcauth.NewValidator(base, keyJSON)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	info, err := val.Introspect(ctx, exchanged)
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	t.Logf("introspect: active=%v subject=%s actorClient=%s actorName=%q",
		info.Active, info.Subject, info.ActorClient, info.ActorName)
}

func kamaraUpload(t *testing.T, ctx context.Context, kamaraURL, token, name, content string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		kamaraURL+"/v1/files?name="+url.QueryEscape(name), strings.NewReader(content))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("kamara upload: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("kamara upload: %d: %s", resp.StatusCode, firstN(string(raw), 300))
	}
	var out struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &out)
	return out.ID
}

func kamaraUserOwns(t *testing.T, ctx context.Context, kamaraURL, userToken, fileID string) bool {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, kamaraURL+"/v1/files", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("kamara list: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	json.Unmarshal(raw, &out)
	for _, f := range out.Files {
		if f.ID == fileID {
			return true
		}
	}
	return false
}

func userinfoSub(t *testing.T, ctx context.Context, base, token string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/oidc/v1/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Sub string `json:"sub"`
	}
	json.Unmarshal(raw, &out)
	if out.Sub == "" {
		t.Fatalf("userinfo: no sub (HTTP %d): %s", resp.StatusCode, firstN(string(raw), 200))
	}
	return out.Sub
}

// projectAudToken creates a machine user with a secret and returns an access
// token carrying the tenant project's audience, plus the user's id (the
// expected `sub` of the exchanged token).
func projectAudToken(t *testing.T, c *Client, ctx context.Context, base, orgID, projID string) (string, string) {
	t.Helper()
	uid, err := c.EnsureMachineUser(ctx, base, orgID, "subj-user")
	if err != nil {
		t.Fatalf("machine user: %v", err)
	}
	var sec struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := c.do(ctx, http.MethodPut, base+"/management/v1/users/"+uid+"/secret", orgID, map[string]any{}, &sec); err != nil {
		t.Fatalf("machine secret: %v", err)
	}
	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"openid urn:zitadel:iam:org:project:id:" + projID + ":aud"},
	}
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(sec.ClientID+":"+sec.ClientSecret))
	// The just-created secret projects asynchronously — retry.
	var lastBody string
	for i := 0; i < 8; i++ {
		time.Sleep(2 * time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth/v2/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", basic)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("subject token: %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var out struct {
			AccessToken string `json:"access_token"`
		}
		json.Unmarshal(raw, &out)
		if out.AccessToken != "" {
			return out.AccessToken, uid
		}
		lastBody = string(raw)
	}
	t.Fatalf("subject token: no access_token: %s", lastBody)
	return "", ""
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
