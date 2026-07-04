package search

import (
	"context"
	"testing"

	"github.com/peristera-io/peristera/lib/pii"
)

// memIndex is an in-memory Index for tests.
type memIndex struct {
	docs map[string]Doc
}

func newMemIndex() *memIndex { return &memIndex{docs: map[string]Doc{}} }

func (m *memIndex) Upsert(_ context.Context, d Doc) error {
	m.docs[d.ID] = d
	return nil
}
func (m *memIndex) Delete(_ context.Context, id string) error {
	delete(m.docs, id)
	return nil
}

func TestFeedUpsertsAndRemove(t *testing.T) {
	ctx := context.Background()
	idx := newMemIndex()
	f := NewFeeder(idx)

	d := Doc{
		ID: "0198c", Type: "ergonomos/task", Permalink: "/tasks/0198c",
		Owner: pii.Subject{Instance: "demo.example", UserID: "alice"},
		Text:  "buy milk",
	}
	if err := f.Feed(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := idx.docs["0198c"]; got.Text != "buy milk" {
		t.Errorf("doc not indexed: %+v", got)
	}
	// Feed is an upsert (idempotent on ID).
	d.Text = "buy oat milk"
	_ = f.Feed(ctx, d)
	if idx.docs["0198c"].Text != "buy oat milk" {
		t.Error("re-feed did not update the doc")
	}
	// Remove drops it.
	if err := f.Remove(ctx, "0198c"); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.docs["0198c"]; ok {
		t.Error("doc not removed")
	}
}

func TestFeedValidates(t *testing.T) {
	f := NewFeeder(newMemIndex())
	owner := pii.Subject{Instance: "demo.example", UserID: "alice"}
	cases := map[string]Doc{
		"no ID":        {Type: "ergonomos/task", Owner: owner, Permalink: "/x"},
		"no Type":      {ID: "x", Owner: owner, Permalink: "/x"},
		"no Owner":     {ID: "x", Type: "ergonomos/task", Permalink: "/x"},
		"no Permalink": {ID: "x", Type: "ergonomos/task", Owner: owner},
	}
	for name, d := range cases {
		if err := f.Feed(context.Background(), d); err == nil {
			t.Errorf("Feed with %s should error", name)
		}
	}
	if err := f.Remove(context.Background(), ""); err == nil {
		t.Error("Remove without id should error")
	}
}
