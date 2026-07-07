package controller

import (
	"strings"
	"testing"
)

// The office engine speaks plain HTTP either way, but the WOPI discovery urlsrc
// scheme follows ssl.termination: https on the cloud (so Kamara's HTTPS /edit
// page embeds an https:// iframe, not a mixed-content http:// one), http in dev
// where Kamara is http too. frame_ancestors is always pinned to Kamara's origin.
func TestOfficeExtraParams(t *testing.T) {
	const origin = "https://kamara.demo.peristera.app"

	dev := officeExtraParams(false, origin)
	if !strings.Contains(dev, "--o:ssl.termination=false") {
		t.Errorf("dev must not claim TLS termination: %q", dev)
	}

	cloud := officeExtraParams(true, origin)
	if !strings.Contains(cloud, "--o:ssl.termination=true") {
		t.Errorf("cloud must claim TLS termination so discovery emits https: %q", cloud)
	}

	for _, p := range []string{dev, cloud} {
		if !strings.Contains(p, "--o:ssl.enable=false") {
			t.Errorf("engine must always speak plain http: %q", p)
		}
		if !strings.Contains(p, "--o:net.frame_ancestors="+origin) {
			t.Errorf("frame_ancestors must be pinned to Kamara's origin: %q", p)
		}
	}
}
