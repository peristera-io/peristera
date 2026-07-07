package wopi

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Discovery resolves the office engine's editor URL for a file type from its
// WOPI discovery document (`/hosting/discovery`, ADR-0018). Collabora bakes
// its own public host into the `urlsrc` (from the request Host), so Kamara
// fetches discovery via the engine's public URL and the returned URL is ready
// to embed. The document changes only on an engine upgrade, so it is cached.
type Discovery struct {
	baseURL string
	client  *http.Client

	mu     sync.Mutex
	byExt  map[string]actions // extension (no dot, lowercased) → actions
	loaded bool
}

// actions holds the urlsrc for the edit and view actions of one file type.
type actions struct {
	edit, view string
}

// NewDiscovery builds a client against the engine's base URL (its public URL).
func NewDiscovery(baseURL string) *Discovery {
	return &Discovery{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// EditURL returns the editor URL to embed for a file with the given extension
// (e.g. "odt"), pointing the engine at wopiSrc. It prefers the edit action and
// falls back to view. The discovery document is fetched once and cached.
func (d *Discovery) EditURL(ctx context.Context, ext, wopiSrc string) (string, error) {
	if err := d.ensureLoaded(ctx); err != nil {
		return "", err
	}
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	d.mu.Lock()
	a, ok := d.byExt[ext]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("wopi: no editor for %q", ext)
	}
	src := a.edit
	if src == "" {
		src = a.view
	}
	if src == "" {
		return "", fmt.Errorf("wopi: no edit/view action for %q", ext)
	}
	return appendWOPISrc(src, wopiSrc), nil
}

// appendWOPISrc appends the WOPISrc query parameter to an urlsrc, which may
// already end with "?" or carry other placeholder params.
func appendWOPISrc(urlsrc, wopiSrc string) string {
	sep := "?"
	switch {
	case strings.HasSuffix(urlsrc, "?"):
		sep = ""
	case strings.Contains(urlsrc, "?"):
		sep = "&"
	}
	return urlsrc + sep + "WOPISrc=" + url.QueryEscape(wopiSrc)
}

func (d *Discovery) ensureLoaded(ctx context.Context) error {
	d.mu.Lock()
	loaded := d.loaded
	d.mu.Unlock()
	if loaded {
		return nil
	}
	byExt, err := d.fetch(ctx)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.byExt, d.loaded = byExt, true
	d.mu.Unlock()
	return nil
}

func (d *Discovery) fetch(ctx context.Context) (map[string]actions, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/hosting/discovery", nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wopi: fetch discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wopi: discovery status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseDiscovery(body)
}

// parseDiscovery collects every <action> element (regardless of nesting) into
// a per-extension edit/view urlsrc map.
func parseDiscovery(doc []byte) (map[string]actions, error) {
	dec := xml.NewDecoder(strings.NewReader(string(doc)))
	out := map[string]actions{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("wopi: parse discovery: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "action" {
			continue
		}
		var ext, name, urlsrc string
		for _, attr := range se.Attr {
			switch attr.Name.Local {
			case "ext":
				ext = strings.ToLower(attr.Value)
			case "name":
				name = attr.Value
			case "urlsrc":
				urlsrc = attr.Value
			}
		}
		if ext == "" || urlsrc == "" {
			continue
		}
		a := out[ext]
		switch name {
		case "edit":
			a.edit = urlsrc
		case "view":
			a.view = urlsrc
		default:
			if a.view == "" { // a sensible fallback for unnamed actions
				a.view = urlsrc
			}
		}
		out[ext] = a
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("wopi: discovery had no actions")
	}
	return out, nil
}
