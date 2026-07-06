// Package kamara is a tiny client for calling Kamara's storage API on behalf
// of a logged-in user (ADR-0017). It exchanges the user's access token for an
// on-behalf-of token (lib/svcauth) and uploads with it, so the file lands
// owned by the user, not the calling service. This is the seed of the
// "Kamara SDK" — a service that wants file storage embeds this and gets
// user-owned uploads for free.
package kamara

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/peristera-io/peristera/lib/svcauth"
)

// Client uploads to Kamara on behalf of a user.
type Client struct {
	baseURL string
	ex      *svcauth.Exchanger
	http    *http.Client
}

// New builds a client for the Kamara at baseURL (its in-cluster service URL),
// exchanging tokens via ex.
func New(baseURL string, ex *svcauth.Exchanger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		ex:      ex,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Upload stores content as name in Kamara, owned by the user whose access
// token is userAccessToken, and returns the new file's id. The user's token
// is exchanged (actor=this service, subject=user) so Kamara authorizes and
// owns the file to the user.
func (c *Client) Upload(ctx context.Context, userAccessToken, name string, content io.Reader) (string, error) {
	tok, err := c.ex.OnBehalfOf(ctx, userAccessToken)
	if err != nil {
		return "", err
	}
	u := c.baseURL + "/v1/files?name=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, content)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("kamara upload: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("kamara upload: %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("kamara upload response: %w", err)
	}
	return out.ID, nil
}
