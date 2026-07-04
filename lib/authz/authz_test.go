package authz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/peristera-io/peristera/lib/pii"
)

func TestObjAndTuple(t *testing.T) {
	if got := Obj("ergonomos/task", "0198c"); got != "ergonomos/task:0198c" {
		t.Errorf("Obj = %q", got)
	}
	u := pii.Subject{Instance: "demo.example", UserID: "alice"}
	tk := tuple(u, "owner", "ergonomos/task:0198c")
	if tk["user"] != "user:demo.example/alice" || tk["relation"] != "owner" ||
		tk["object"] != "ergonomos/task:0198c" {
		t.Errorf("tuple = %+v", tk)
	}
}

// fakeFGA is a minimal OpenFGA stand-in exercising the request shapes
// Connect/Check/ListObjects produce, so the wiring is tested without a
// real server.
func TestClientAgainstFakeServer(t *testing.T) {
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	var wroteTuple bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/stores":
			_, _ = w.Write([]byte(`{"stores":[]}`))
		case r.Method == "POST" && r.URL.Path == "/stores":
			_, _ = w.Write([]byte(`{"id":"store1"}`))
		case r.URL.Path == "/stores/store1/authorization-models":
			_, _ = w.Write([]byte(`{"authorization_model_id":"model1"}`))
		case r.URL.Path == "/stores/store1/write":
			wroteTuple = true
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/stores/store1/check":
			_, _ = w.Write([]byte(`{"allowed":true}`))
		case r.URL.Path == "/stores/store1/list-objects":
			_, _ = w.Write([]byte(`{"objects":["ergonomos/task:0198c","ergonomos/task:0199d"]}`))
		default:
			http.Error(w, "unexpected "+r.URL.Path, 500)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	c, err := Connect(ctx, srv.URL, "ergonomos", json.RawMessage(`{"schema_version":"1.1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.storeID != "store1" || c.modelID != "model1" {
		t.Fatalf("store/model = %q/%q", c.storeID, c.modelID)
	}
	if err := c.Write(ctx, alice, "owner", Obj("ergonomos/task", "0198c")); err != nil || !wroteTuple {
		t.Errorf("Write failed: %v wrote=%v", err, wroteTuple)
	}
	ok, err := c.Check(ctx, alice, "owner", Obj("ergonomos/task", "0198c"))
	if err != nil || !ok {
		t.Errorf("Check = %v,%v", ok, err)
	}
	ids, err := c.ListObjects(ctx, alice, "owner", "ergonomos/task")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "0198c" || ids[1] != "0199d" {
		t.Errorf("ListObjects stripped ids = %v", ids)
	}
}
