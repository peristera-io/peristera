package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func cert(ready string, fails *int64) *unstructured.Unstructured {
	status := map[string]any{
		"conditions": []any{
			map[string]any{"type": "Ready", "status": ready},
		},
	}
	if fails != nil {
		status["failedIssuanceAttempts"] = *fails
	}
	return &unstructured.Unstructured{Object: map[string]any{"status": status}}
}

func i64(v int64) *int64 { return &v }

// A cert is only "stuck" (eligible for reset) when it is not Ready AND
// cert-manager has recorded at least one failed issuance — so a still-issuing
// first attempt (no failures) is never reset, protecting the LE rate limit.
func TestCertStuck(t *testing.T) {
	cases := []struct {
		name  string
		ready string
		fails *int64
		want  bool
	}{
		{"ready cert is never stuck", "True", i64(3), false},
		{"issuing, no failures yet -> not stuck", "False", i64(0), false},
		{"issuing, failedAttempts absent -> not stuck", "False", nil, false},
		{"failed once -> stuck", "False", i64(1), true},
		{"failed repeatedly -> stuck", "False", i64(5), true},
	}
	for _, c := range cases {
		if got := certStuck(cert(c.ready, c.fails)); got != c.want {
			t.Errorf("%s: certStuck = %v, want %v", c.name, got, c.want)
		}
	}
}
