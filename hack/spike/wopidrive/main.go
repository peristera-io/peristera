// Command wopidrive headlessly drives a Collabora document load over the
// "cool" WebSocket protocol, so we can confirm coolwsd calls our WOPI host
// (CheckFileInfo/GetFile) and accepts a save (PutFile) — no browser needed.
//
// Usage: wopidrive <collabora-ws-base> <wopi-src> <access-token>
//
//	e.g. wopidrive ws://localhost:9980 http://stub...:8080/wopi/files/doc1 tok
package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"nhooyr.io/websocket"
)

func main() {
	base, wopiSrc, token := os.Args[1], os.Args[2], os.Args[3]

	// cool WS URL: /cool/<url-encoded-WOPISrc>/ws?WOPISrc=..&compat=/ws
	enc := url.QueryEscape(wopiSrc)
	// Modern cool reads the WOPI access_token from the WS URL query string.
	wsURL := fmt.Sprintf("%s/cool/%s/ws?WOPISrc=%s&access_token=%s&compat=/ws",
		base, enc, enc, url.QueryEscape(token))
	log.Printf("dialing %s", wsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// coolwsd rejects WS upgrades whose Origin isn't the Collabora host itself
	// (in a browser the editor iframe is served from Collabora, so Origin
	// matches). Mirror that here.
	origin := base
	if len(origin) > 3 && origin[:2] == "ws" {
		origin = "http" + origin[2:] // ws->http, wss->https
	}
	opts := &websocket.DialOptions{HTTPHeader: map[string][]string{"Origin": {origin}}}
	c, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "done")

	send := func(msg string) {
		log.Printf(">> %s", msg)
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			log.Fatalf("write: %v", err)
		}
	}

	// Protocol handshake + load. access_token is carried on the WS URL query in
	// modern cool, but we also pass it on the load line for older builds.
	send("coolclient 0.1 0.1")
	send(fmt.Sprintf("load url=%s accessToken=%s accessTokenTtl=0", wopiSrc, token))

	// Read frames for a while; the interesting ones are 'status:' (doc loaded)
	// and any 'error:'.  After load succeeds, ask for a save to exercise PutFile.
	deadline := time.Now().Add(30 * time.Second)
	askedSave, inited := false, false
	for time.Now().Before(deadline) {
		rctx, rcancel := context.WithTimeout(ctx, 8*time.Second)
		typ, data, err := c.Read(rctx)
		rcancel()
		if err != nil {
			log.Printf("read ended: %v", err)
			break
		}
		msg := string(data)
		if typ == websocket.MessageBinary {
			log.Printf("<< [binary %d bytes]", len(data))
			continue
		}
		if len(msg) > 120 {
			msg = msg[:120] + "…"
		}
		log.Printf("<< %s", msg)
		// Only after the doc has actually loaded does the kit view accept the
		// geometry init; sending it earlier yields 'nodocloaded'.
		if !inited && contains(msg, "commandresult:") && contains(msg, "\"load\"") {
			inited = true
			send("clientvisiblearea x=0 y=0 width=15875 height=10583")
			send("clientzoom tilepixelwidth=256 tilepixelheight=256 tiletwipwidth=3840 tiletwipheight=3840")
		}
		// 'status:' carries the doc geometry and means the kit view is ready.
		if !askedSave && contains(msg, "status:") {
			askedSave = true
			// Paste text to dirty the document, then save -> triggers PutFile.
			send("paste mimetype=text/plain;charset=utf-8\nEDITED-BY-SPIKE")
			time.Sleep(1 * time.Second)
			send("save dontTerminateEdit=1 dontSaveIfUnmodified=0 extendedData=")
		}
	}
	log.Printf("done")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
