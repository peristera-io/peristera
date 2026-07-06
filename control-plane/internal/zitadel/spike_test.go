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
	if err := c.EnableImpersonation(ctx, base); err != nil {
		t.Fatalf("enable impersonation: %v", err)
	}

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
	subjTok := projectAudToken(t, c, ctx, base, orgID, projID)

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
	t.Logf("OK — exchanged token (%d chars) via svcauth", len(exchanged))
}

// projectAudToken creates a machine user with a secret and returns an access
// token carrying the tenant project's audience.
func projectAudToken(t *testing.T, c *Client, ctx context.Context, base, orgID, projID string) string {
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
			return out.AccessToken
		}
		lastBody = string(raw)
	}
	t.Fatalf("subject token: no access_token: %s", lastBody)
	return ""
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
