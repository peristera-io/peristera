package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/session"
)

// Operator authorization model (ADR-0019): a singleton platform object with an
// `operator` relation of user. The check is platform:peristera#operator@user.
const (
	platformObject   = "platform:peristera"
	operatorRelation = "operator"
)

var operatorModel = json.RawMessage(`{
  "schema_version": "1.1",
  "type_definitions": [
    {"type": "user"},
    {"type": "platform",
     "relations": {"operator": {"this": {}}},
     "metadata": {"relations": {"operator": {"directly_related_user_types": [{"type": "user"}]}}}}
  ]
}`)

// tokenCache remembers a validated bearer token → subject for a short TTL so
// every API call doesn't re-hit userinfo. Backed by lib/session (it evicts).
type tokenCache struct {
	store *session.Store[pii.Subject]
}

func newTokenCache(ttl time.Duration) *tokenCache {
	return &tokenCache{store: session.NewStore[pii.Subject](ttl)}
}

func (t *tokenCache) get(tok string) (pii.Subject, bool) { return t.store.Get(tok) }
func (t *tokenCache) put(tok string, s pii.Subject)      { t.store.Put(tok, s) }

// requireAuth guards a handler with the two ADR-0019 gates: the request must
// resolve to a subject whose credential is *for* the control plane (audience),
// and that subject must be an operator in the platform OpenFGA. A missing
// credential is a 401/login; a valid non-operator is a 403.
func (s *Server) requireAuth(next http.Handler, isAPI bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject, ok := s.operatorSubject(r.Context(), r)
		if !ok {
			s.denyUnauthenticated(w, r, isAPI)
			return
		}
		allowed, err := s.authz.Check(r.Context(), subject, operatorRelation, platformObject)
		if err != nil {
			// Authorization store unreachable — fail closed, don't leak detail.
			s.writeStatus(w, http.StatusBadGateway, "authorization unavailable")
			return
		}
		if !allowed {
			// Authenticated, but not an operator.
			s.writeStatus(w, http.StatusForbidden, "not authorized as an operator")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// operatorSubject resolves a request to an authenticated subject: a browser
// session (audience-correct via oidcrp) or a bearer token (userinfo-validated
// and, when a JWT, audience-checked against the control-plane client — opaque
// tokens fall through to the operator check, ADR-0019).
func (s *Server) operatorSubject(ctx context.Context, r *http.Request) (pii.Subject, bool) {
	inst := issuerHost(s.Cfg.Issuer)
	if sess, ok := s.rp.Session(r); ok {
		return pii.Subject{Instance: inst, UserID: sess.Claims.Subject}, true
	}
	auth := r.Header.Get("Authorization")
	if len(auth) <= 7 || !strings.EqualFold(auth[:7], "Bearer ") {
		return pii.Subject{}, false
	}
	tok := strings.TrimSpace(auth[7:])
	if !audienceOK(tok, s.clientID) {
		return pii.Subject{}, false // a JWT minted for another audience
	}
	if sub, ok := s.tokens.get(tok); ok {
		return sub, true
	}
	sub, ok := s.IAM.UserinfoSubject(ctx, s.Cfg.Issuer, tok)
	if !ok {
		return pii.Subject{}, false
	}
	subject := pii.Subject{Instance: inst, UserID: sub}
	s.tokens.put(tok, subject)
	return subject, true
}

func (s *Server) denyUnauthenticated(w http.ResponseWriter, r *http.Request, isAPI bool) {
	if isAPI {
		s.writeStatus(w, http.StatusUnauthorized, "authentication required")
		return
	}
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

func (s *Server) writeStatus(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// audienceOK reports whether a bearer token may target the control plane. A
// JWT is accepted only if its `aud` includes clientID; a JWT with a definite,
// different audience is rejected. Opaque tokens (and unparseable/aud-less ones)
// return true — they carry no locally-checkable audience, so the operator check
// is their gate (ADR-0019; full introspection is the MSP-alpha upgrade).
func audienceOK(token, clientID string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return true // opaque
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return true
	}
	var claims struct {
		Aud json.RawMessage `json:"aud"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return true
	}
	auds := parseAud(claims.Aud)
	if len(auds) == 0 {
		return true // no audience claim to check against
	}
	for _, a := range auds {
		if a == clientID {
			return true
		}
	}
	return false
}

// parseAud accepts the JWT `aud` in either form: a string or an array of
// strings.
func parseAud(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

// user returns the current browser session, if any (the UI shows who's on).
func (s *Server) user(r *http.Request) (oidcrp.Session, bool) {
	return s.rp.Session(r)
}

func issuerHost(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil {
		return issuer
	}
	return u.Hostname()
}
