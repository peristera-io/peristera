package pii

import (
	"testing"
	"time"
)

func TestNoneNeverBlocks(t *testing.T) {
	now := time.Now()
	if None.BlocksErasureAt(now.Add(-time.Hour), now, false) {
		t.Error("None must never block erasure")
	}
	// ...but a legal hold blocks even None.
	if !None.BlocksErasureAt(now, now, true) {
		t.Error("a legal hold must block erasure regardless of class")
	}
}

func TestRetentionMinDuration(t *testing.T) {
	c := RetentionClass{Name: "accounting-10y-lu", Min: 10 * 365 * 24 * time.Hour, LegalBasis: "LU accounting law"}
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	within := anchor.Add(5 * 365 * 24 * time.Hour)
	if !c.BlocksErasureAt(anchor, within, false) {
		t.Error("must block erasure within the retention window")
	}
	after := anchor.Add(11 * 365 * 24 * time.Hour)
	if c.BlocksErasureAt(anchor, after, false) {
		t.Error("must allow erasure after the retention window")
	}
	// Boundary: exactly at anchor+Min, erasure is allowed (Before is
	// exclusive) — pin the off-by-one semantics.
	exactly := anchor.Add(c.Min)
	if c.BlocksErasureAt(anchor, exactly, false) {
		t.Error("erasure must be allowed exactly at the retention expiry")
	}
}

func TestRegisterClass(t *testing.T) {
	resetClasses()
	defer resetClasses()
	if _, ok := Class("none"); !ok {
		t.Fatal("None must be pre-registered")
	}
	RegisterClass(RetentionClass{Name: "test-contract", Min: time.Hour})
	if _, ok := Class("test-contract"); !ok {
		t.Error("registered class not found")
	}
	defer func() {
		if recover() == nil {
			t.Error("re-registering a class must panic")
		}
	}()
	RegisterClass(RetentionClass{Name: "test-contract"})
}
