package pii

import "time"

// RetentionClass is the erasure mirror (ADR-0009 §4): how long data of a
// kind must be *kept*, and why. Erasure refuses or defers what a class (or
// an active legal hold) requires keeping.
//
// M3 ships the mechanism and the None default only; populated classes
// (e.g. accounting-10y-lu, contract, employment) and hold enforcement are
// deferred until a real retention requirement lands.
type RetentionClass struct {
	Name string
	// Min is the minimum time data must be kept from its retention anchor.
	// Zero means "no minimum" (the None class).
	Min time.Duration
	// LegalBasis is a short human-readable justification, for the
	// processing registry and for explaining a refused erasure.
	LegalBasis string
}

// None is the default: nothing is required to be kept, so erasure is never
// blocked by retention.
var None = RetentionClass{Name: "none"}

// classes shares regMu (registry.go) with the descriptor registry — both
// are init-time taxonomies read at runtime; one lock keeps them consistent.
var classes = map[string]RetentionClass{None.Name: None}

// RegisterClass adds a retention class. Registering a name twice panics —
// classes are a fixed, opinionated taxonomy, not runtime configuration.
func RegisterClass(c RetentionClass) {
	if c.Name == "" {
		panic("pii: retention class needs a name")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := classes[c.Name]; dup {
		panic("pii: retention class already registered: " + c.Name)
	}
	classes[c.Name] = c
}

// Class looks up a registered retention class.
func Class(name string) (RetentionClass, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	c, ok := classes[name]
	return c, ok
}

// BlocksErasureAt reports whether this class still requires the data to be
// kept at time now, given the retention anchor (e.g. record creation or
// contract end) and whether a legal hold is active. A hold always blocks.
func (c RetentionClass) BlocksErasureAt(anchor, now time.Time, legalHold bool) bool {
	if legalHold {
		return true
	}
	if c.Min == 0 {
		return false
	}
	return now.Before(anchor.Add(c.Min))
}
