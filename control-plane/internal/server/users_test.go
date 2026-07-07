package server

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGenPassword(t *testing.T) {
	a, err := genPassword()
	if err != nil {
		t.Fatal(err)
	}
	// Must satisfy Zitadel's default complexity (upper, lower, digit, symbol);
	// the "Aa1!" prefix guarantees it regardless of the random tail.
	if !strings.HasPrefix(a, "Aa1!") {
		t.Errorf("password %q lacks the complexity prefix", a)
	}
	if len(a) < 12 {
		t.Errorf("password %q too short", a)
	}
	b, _ := genPassword()
	if a == b {
		t.Error("two generated passwords are identical")
	}
}

// A malformed email is rejected before the tenant is even looked up, so this
// needs no cluster — it guards the 400 path of both the API and UI handlers.
func TestCreateTenantUserRejectsBadEmail(t *testing.T) {
	for _, bad := range []string{"", "notanemail", "@nolocal.com", "nodomain@", "  "} {
		_, _, err := (&Server{}).createTenantUser(context.Background(), "demo", bad)
		if !errors.Is(err, errBadEmail) {
			t.Errorf("email %q: got %v, want errBadEmail", bad, err)
		}
	}
}
