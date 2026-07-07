// Command wopistub is a throwaway WOPI host for the M6 Collabora spike.
// It implements the three WOPI endpoints Collabora drives:
//
//	GET  /wopi/files/{id}            -> CheckFileInfo (JSON metadata)
//	GET  /wopi/files/{id}/contents   -> GetFile (raw bytes)
//	POST /wopi/files/{id}/contents   -> PutFile (save-back)
//
// It logs every hit so we can confirm coolwsd actually calls back server-side,
// and it enforces a per-session access_token to mirror the real Kamara design.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// A single in-memory doc, seeded with a tiny ODT so LibreOffice opens it.
var (
	mu       sync.Mutex
	content  = seedODT()
	version  = "v0"
	saves    int
	theToken = "spike-token-abc123"
	relax    = os.Getenv("RELAX") == "1"
)

func main() {
	http.HandleFunc("/wopi/files/", handle)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	log.Println("wopistub listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handle(w http.ResponseWriter, r *http.Request) {
	// WOPI hosts may receive the token as a query param OR an Authorization
	// Bearer header (coolwsd uses the latter). Accept both.
	tok := r.URL.Query().Get("access_token")
	if tok == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			tok = strings.TrimPrefix(a, "Bearer ")
		}
	}
	log.Printf("HIT %s %s  token=%q  authz=%q  proof=%v", r.Method, r.URL.Path, tok,
		r.Header.Get("Authorization"), r.Header.Get("X-WOPI-Proof") != "")
	if relax {
		// spike mode: skip token check to prove the raw round-trip
	} else if tok != theToken {
		log.Printf("  -> 401 bad token")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/wopi/files/")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/contents"):
		getFile(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/contents"):
		putFile(w, r)
	case r.Method == http.MethodGet:
		checkFileInfo(w, r, strings.TrimSuffix(path, "/contents"))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func checkFileInfo(w http.ResponseWriter, r *http.Request, id string) {
	mu.Lock()
	defer mu.Unlock()
	info := map[string]any{
		"BaseFileName":     "spike.odt",
		"Size":             len(content),
		"Version":          version,
		"OwnerId":          "owner-1",
		"UserId":           "user-1",
		"UserFriendlyName": "Spike User",
		"UserCanWrite":     true,
		"UserCanRename":    false,
		"SupportsUpdate":   true,
		"SupportsLocks":    false,
		"LastModifiedTime": time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
	log.Printf("  -> CheckFileInfo size=%d version=%s", len(content), version)
}

func getFile(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(content)
	log.Printf("  -> GetFile served %d bytes", len(content))
}

func putFile(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	mu.Lock()
	defer mu.Unlock()
	content = body
	saves++
	version = fmt.Sprintf("v%d", saves)
	sum := sha256.Sum256(body)
	// WOPI expects the new version echoed back so the client can track it.
	w.Header().Set("X-WOPI-ItemVersion", version)
	w.WriteHeader(http.StatusOK)
	log.Printf("  -> PutFile SAVED %d bytes  sha256=%s  newversion=%s",
		len(body), base64.StdEncoding.EncodeToString(sum[:8]), version)
}

// seedODT builds a minimal, valid ODT (a zip container with an uncompressed
// mimetype entry, content.xml, and a manifest) so LibreOffice can open it.
func seedODT() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// mimetype MUST be first and stored (uncompressed) per the ODF spec.
	mw, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	mw.Write([]byte("application/vnd.oasis.opendocument.text"))

	content := `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content
 xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
 xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"
 office:version="1.2">
 <office:body><office:text>
  <text:p>Peristera M6 spike seed document.</text:p>
 </office:text></office:body>
</office:document-content>`
	cw, _ := zw.Create("content.xml")
	cw.Write([]byte(content))

	manifest := `<?xml version="1.0" encoding="UTF-8"?>
<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.2">
 <manifest:file-entry manifest:full-path="/" manifest:media-type="application/vnd.oasis.opendocument.text"/>
 <manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>
</manifest:manifest>`
	nw, _ := zw.Create("META-INF/manifest.xml")
	nw.Write([]byte(manifest))

	zw.Close()
	return buf.Bytes()
}
