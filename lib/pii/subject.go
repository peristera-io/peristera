// Package pii implements Peristera's personal-data metadata contract
// (ADR-0009): the "which person, how to export, how to delete, how long to
// keep" questions every app must answer at the model level. It is a
// convention library — apps register descriptors and implement the
// export/erase hooks; the whole-tenant orchestration lives elsewhere.
package pii

import (
	"fmt"
	"strings"
)

// Subject is a data subject — the natural person a personal-data record
// relates to. The canonical form (ADR-0009 §2, pinned and permanent) is
//
//	<home-instance-domain>/<user-id>
//
// e.g. "demo.127.0.0.1.sslip.io/2789…". The instance domain is the
// tenant's permanent OIDC issuer domain (ADR-0006 §2), which makes the
// identifier federation-ready: a subject may live on another instance.
//
// Instance and UserID are trusted-caller inputs — they come from the IdP
// (the tenant's Zitadel issuer domain and user ID), not from end-user
// text, so this package does not sanitize them beyond the non-empty parse.
type Subject struct {
	Instance string // home-instance domain, e.g. demo.127.0.0.1.sslip.io
	UserID   string // opaque within its instance
}

// ParseSubject parses the canonical "<instance>/<user-id>" form. The
// instance is everything before the first slash; the user-id is the
// remainder (which may itself contain slashes, though Zitadel IDs do not).
func ParseSubject(s string) (Subject, error) {
	instance, userID, ok := strings.Cut(s, "/")
	if !ok || instance == "" || userID == "" {
		return Subject{}, fmt.Errorf("pii: %q is not a canonical subject (<instance>/<user-id>)", s)
	}
	return Subject{Instance: instance, UserID: userID}, nil
}

// String renders the canonical form.
func (s Subject) String() string {
	return s.Instance + "/" + s.UserID
}

// OpenFGAObject renders the subject as an OpenFGA object of type user
// (ADR-0010 §3): "user:<instance>/<user-id>".
func (s Subject) OpenFGAObject() string {
	return "user:" + s.String()
}

// Zero reports whether the subject is unset.
func (s Subject) Zero() bool {
	return s.Instance == "" && s.UserID == ""
}
