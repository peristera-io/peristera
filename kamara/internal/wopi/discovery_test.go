package wopi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A trimmed but realistic Collabora discovery document (the urlsrc carries the
// engine's own public host, as coolwsd emits from the request Host).
const sampleDiscovery = `<?xml version="1.0" encoding="utf-8"?>
<wopi-discovery>
 <net-zone name="external-http">
  <app name="writer">
   <action ext="odt" name="edit" default="true" urlsrc="http://office.example:9080/browser/de013a57f9/cool.html?"/>
   <action ext="docx" name="edit" urlsrc="http://office.example:9080/browser/de013a57f9/cool.html?"/>
   <action ext="pdf" name="view" urlsrc="http://office.example:9080/browser/de013a57f9/cool.html?"/>
  </app>
 </net-zone>
</wopi-discovery>`

func TestParseAndEditURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hosting/discovery" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(sampleDiscovery))
	}))
	defer srv.Close()

	d := NewDiscovery(srv.URL)
	got, err := d.EditURL(context.Background(), "odt", "http://kamara.svc/wopi/files/f1")
	if err != nil {
		t.Fatal(err)
	}
	want := "http://office.example:9080/browser/de013a57f9/cool.html?WOPISrc=" +
		"http%3A%2F%2Fkamara.svc%2Fwopi%2Ffiles%2Ff1"
	if got != want {
		t.Errorf("EditURL =\n %s\nwant\n %s", got, want)
	}

	// A leading dot on the extension is tolerated; a view-only type falls back
	// to the view action.
	if _, err := d.EditURL(context.Background(), ".pdf", "http://k/wopi/files/f2"); err != nil {
		t.Errorf("pdf (view fallback): %v", err)
	}
	// An unknown type is a clean error, not a panic.
	if _, err := d.EditURL(context.Background(), "xyz", "http://k/wopi/files/f3"); err == nil {
		t.Error("expected error for unknown extension")
	}
}

func TestDiscoveryFetchedOnce(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(sampleDiscovery))
	}))
	defer srv.Close()
	d := NewDiscovery(srv.URL)
	for i := 0; i < 3; i++ {
		if _, err := d.EditURL(context.Background(), "odt", "http://k/wopi/files/f1"); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Errorf("discovery fetched %d times, want 1 (cached)", hits)
	}
}

func TestDiscoveryFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	d := NewDiscovery(srv.URL)
	if _, err := d.EditURL(context.Background(), "odt", "x"); err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("expected a status error, got %v", err)
	}
}
