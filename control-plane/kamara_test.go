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

// kamaraTok mints (once per tenant) a PAT for a machine user in the
// tenant's own Zitadel instance — a default-instance token would not pass
// Kamara's userinfo check against the tenant issuer.
func (w *world) kamaraTok(slug string) (string, error) {
	if t := w.kamaraToks[slug]; t != "" {
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
	userID, err := iam.EnsureMachineUser(ctx, issuer, orgID, "kamara-smoke")
	if err != nil {
		return "", fmt.Errorf("machine user: %w", err)
	}
	pat, err := iam.CreatePAT(ctx, issuer, orgID, userID)
	if err != nil {
		return "", fmt.Errorf("PAT: %w", err)
	}
	w.kamaraToks[slug] = pat
	return pat, nil
}

func (w *world) kamaraReq(method, slug, path string, body io.Reader) (*http.Response, error) {
	tok, err := w.kamaraTok(slug)
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
	resp, err := w.kamaraReq(http.MethodGet, slug, "/v1/files", nil)
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
