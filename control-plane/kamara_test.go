// Kamara storage-API acceptance steps for the godog suite (M4a, Q&A R41):
// a caller with a token from the tenant's OWN issuer round-trips a file
// through the deployed storage API. Kamara validates the bearer against the
// tenant issuer's userinfo, so the token is minted in the tenant Zitadel
// instance (not the default one). This is the ordinary user-auth path;
// service-to-service trust is a separate, deferred design (issue #29).
package controlplane_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// kamaraHealthy polls Kamara's /healthz — the right readiness probe for an
// API service (it serves no "/" UI in M4a; the browser UI is M4b).
func (w *world) kamaraHealthy(slug string, minutes int) error {
	url := w.appURL(slug, "kamara") + "/healthz"
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Sprintf("status %d", resp.StatusCode)
		} else {
			last = err.Error()
		}
		time.Sleep(w.pollInterval)
	}
	return fmt.Errorf("kamara of %s never became healthy on %s: %s", slug, url, last)
}

func (w *world) tenantIssuer(slug string) (string, error) {
	t := &v1alpha1.Tenant{}
	if err := w.k8s.Get(context.Background(), client.ObjectKey{Name: slug}, t); err != nil {
		return "", err
	}
	if t.Status.Issuer == "" {
		return "", fmt.Errorf("tenant %q has no issuer yet", slug)
	}
	return t.Status.Issuer, nil
}

// kamaraTokAs mints (once per tenant+user) a PAT for a named machine user
// in the tenant's own Zitadel instance — a default-instance token would not
// pass Kamara's userinfo check against the tenant issuer. Two distinct
// usernames give two distinct subjects, which is how the isolation check
// (an intruder can't reach the owner's file) gets a second identity.
func (w *world) kamaraTokAs(slug, username string) (string, error) {
	key := slug + "/" + username
	if t := w.kamaraToks[key]; t != "" {
		return t, nil
	}
	issuer, err := w.tenantIssuer(slug)
	if err != nil {
		return "", err
	}
	iam, err := w.iamClient()
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	orgID, err := iam.FirstOrgID(ctx, issuer)
	if err != nil {
		return "", fmt.Errorf("org in tenant issuer: %w", err)
	}
	userID, err := iam.EnsureMachineUser(ctx, issuer, orgID, username)
	if err != nil {
		return "", fmt.Errorf("machine user %q: %w", username, err)
	}
	pat, err := iam.CreatePAT(ctx, issuer, orgID, userID)
	if err != nil {
		return "", fmt.Errorf("PAT for %q: %w", username, err)
	}
	w.kamaraToks[key] = pat
	return pat, nil
}

// kamaraTok is the file owner's token ("kamara-smoke").
func (w *world) kamaraTok(slug string) (string, error) {
	return w.kamaraTokAs(slug, "kamara-smoke")
}

func (w *world) kamaraReqAs(username, method, slug, path string, body io.Reader) (*http.Response, error) {
	tok, err := w.kamaraTokAs(slug, username)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, w.appURL(slug, "kamara")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return http.DefaultClient.Do(req)
}

// kamaraReq acts as the file owner.
func (w *world) kamaraReq(method, slug, path string, body io.Reader) (*http.Response, error) {
	return w.kamaraReqAs("kamara-smoke", method, slug, path, body)
}

func (w *world) kamaraUpload(contents, name, slug string) error {
	resp, err := w.kamaraReq(http.MethodPost, slug, "/v1/files?name="+name, strings.NewReader(contents))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload: status %d: %s", resp.StatusCode, b)
	}
	var f struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		return err
	}
	if f.ID == "" {
		return fmt.Errorf("upload returned no id")
	}
	w.kamaraFile = f.ID
	return nil
}

func (w *world) kamaraListIDs(slug string) ([]string, error) {
	return w.kamaraListIDsAs("kamara-smoke", slug)
}

func (w *world) kamaraListIDsAs(username, slug string) ([]string, error) {
	resp, err := w.kamaraReqAs(username, http.MethodGet, slug, "/v1/files", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list: status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Files))
	for _, f := range out.Files {
		ids = append(ids, f.ID)
	}
	return ids, nil
}

func (w *world) kamaraFileListed(slug string) error {
	ids, err := w.kamaraListIDs(slug)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == w.kamaraFile {
			return nil
		}
	}
	return fmt.Errorf("file %s not in list %v", w.kamaraFile, ids)
}

func (w *world) kamaraFileNotListed(slug string) error {
	ids, err := w.kamaraListIDs(slug)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == w.kamaraFile {
			return fmt.Errorf("file %s still listed after delete", w.kamaraFile)
		}
	}
	return nil
}

func (w *world) kamaraDownloadEquals(slug, want string) error {
	resp, err := w.kamaraReq(http.MethodGet, slug, "/v1/files/"+w.kamaraFile+"/content", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if string(b) != want {
		return fmt.Errorf("download = %q, want %q", b, want)
	}
	return nil
}

// kamaraIntruderDenied proves cross-subject isolation live through real
// OpenFGA: a second tenant user (no owner tuple) is forbidden from reading,
// downloading, or deleting the owner's file, and never sees it in a listing.
// Existence is not leaked — the authz Check runs before any load, so a
// non-owner gets 403, not 404. Runs while the file still exists (before the
// owner's delete step).
func (w *world) kamaraIntruderDenied(slug string) error {
	const who = "kamara-intruder"
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/files/" + w.kamaraFile},
		{http.MethodGet, "/v1/files/" + w.kamaraFile + "/content"},
		{http.MethodDelete, "/v1/files/" + w.kamaraFile},
	} {
		resp, err := w.kamaraReqAs(who, tc.method, slug, tc.path, nil)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			return fmt.Errorf("intruder %s %s: status %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
	}
	ids, err := w.kamaraListIDsAs(who, slug)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == w.kamaraFile {
			return fmt.Errorf("intruder's listing leaked the owner's file %s", w.kamaraFile)
		}
	}
	return nil
}

func (w *world) kamaraDelete(slug string) error {
	resp, err := w.kamaraReq(http.MethodDelete, slug, "/v1/files/"+w.kamaraFile, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete: status %d", resp.StatusCode)
	}
	return nil
}
