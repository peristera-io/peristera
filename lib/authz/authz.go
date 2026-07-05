// Package authz is the OpenFGA authorization convention (ADR-0010): one
// OpenFGA per tenant, a shared model each app contributes app-namespaced
// types to, subjects that are instance-namespaced users (federation-ready),
// and access decided ONLY by OpenFGA — never an app ownership column.
//
// It talks to OpenFGA's HTTP API directly (like lib's other service
// clients), so there is no SDK version to track.
package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/peristera-io/peristera/lib/pii"
)

// Client is a connected OpenFGA store + model.
type Client struct {
	base    string
	storeID string
	modelID string
	http    *http.Client
}

// Object names an OpenFGA object as "<app-namespaced-type>:<id>", e.g.
// "ergonomos/task:0198c…". Build one with Obj.
func Obj(typ, id string) string { return typ + ":" + id }

// Connect ensures a store named storeName and the given authorization
// model exist (idempotent), returning a ready Client. modelJSON is the
// OpenFGA 1.1 model (type_definitions); an app passes its own type module.
func Connect(ctx context.Context, apiURL, storeName string, modelJSON json.RawMessage) (*Client, error) {
	c := &Client{base: apiURL, http: &http.Client{Timeout: 15 * time.Second}}

	storeID, err := c.findStore(ctx, storeName)
	if err != nil {
		return nil, err
	}
	if storeID == "" {
		if storeID, err = c.createStore(ctx, storeName); err != nil {
			return nil, err
		}
	}
	c.storeID = storeID

	modelID, err := c.writeModel(ctx, modelJSON)
	if err != nil {
		return nil, err
	}
	c.modelID = modelID
	return c, nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("openfga %s %s: %d: %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) findStore(ctx context.Context, name string) (string, error) {
	var out struct {
		Stores []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stores"`
	}
	if err := c.do(ctx, http.MethodGet, "/stores", nil, &out); err != nil {
		return "", err
	}
	for _, s := range out.Stores {
		if s.Name == name {
			return s.ID, nil
		}
	}
	return "", nil
}

func (c *Client) createStore(ctx context.Context, name string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/stores", map[string]any{"name": name}, &out)
	return out.ID, err
}

func (c *Client) writeModel(ctx context.Context, model json.RawMessage) (string, error) {
	var out struct {
		AuthorizationModelID string `json:"authorization_model_id"`
	}
	err := c.do(ctx, http.MethodPost, "/stores/"+c.storeID+"/authorization-models", model, &out)
	return out.AuthorizationModelID, err
}

// Write records that user has relation on object (e.g. owner on a task).
func (c *Client) Write(ctx context.Context, user pii.Subject, relation, object string) error {
	return c.do(ctx, http.MethodPost, "/stores/"+c.storeID+"/write", map[string]any{
		"writes": map[string]any{"tuple_keys": []any{tuple(user, relation, object)}},
	}, nil)
}

// Delete removes a relation tuple (e.g. on object deletion).
func (c *Client) Delete(ctx context.Context, user pii.Subject, relation, object string) error {
	return c.do(ctx, http.MethodPost, "/stores/"+c.storeID+"/write", map[string]any{
		"deletes": map[string]any{"tuple_keys": []any{tuple(user, relation, object)}},
	}, nil)
}

// WriteObjectTuple records an object-to-object relation — the "user" is a
// fully-qualified object (e.g. "kamara/folder:<id>"), not a subject. Used
// for containment edges (a file/folder's parent folder) that drive
// inherited access. Build the object with Obj.
func (c *Client) WriteObjectTuple(ctx context.Context, user, relation, object string) error {
	return c.do(ctx, http.MethodPost, "/stores/"+c.storeID+"/write", map[string]any{
		"writes": map[string]any{"tuple_keys": []any{rawTuple(user, relation, object)}},
	}, nil)
}

// DeleteObjectTuple removes an object-to-object relation (e.g. on move or
// delete). See WriteObjectTuple.
func (c *Client) DeleteObjectTuple(ctx context.Context, user, relation, object string) error {
	return c.do(ctx, http.MethodPost, "/stores/"+c.storeID+"/write", map[string]any{
		"deletes": map[string]any{"tuple_keys": []any{rawTuple(user, relation, object)}},
	}, nil)
}

// Check answers whether user has relation on object — the authorization
// decision. OpenFGA is the sole source of truth (ADR-0010 §4).
func (c *Client) Check(ctx context.Context, user pii.Subject, relation, object string) (bool, error) {
	var out struct {
		Allowed bool `json:"allowed"`
	}
	err := c.do(ctx, http.MethodPost, "/stores/"+c.storeID+"/check", map[string]any{
		"authorization_model_id": c.modelID,
		"tuple_key":              tuple(user, relation, object),
	}, &out)
	return out.Allowed, err
}

// ListObjects returns the object IDs of objectType that user has relation
// on — the permission-filtered listing (ADR-0010 §4). Caveat (§6): OpenFGA
// may bound the result; callers must not assume exhaustiveness at scale.
func (c *Client) ListObjects(ctx context.Context, user pii.Subject, relation, objectType string) ([]string, error) {
	var out struct {
		Objects []string `json:"objects"`
	}
	err := c.do(ctx, http.MethodPost, "/stores/"+c.storeID+"/list-objects", map[string]any{
		"authorization_model_id": c.modelID,
		"type":                   objectType,
		"relation":               relation,
		"user":                   user.OpenFGAObject(),
	}, &out)
	if err != nil {
		return nil, err
	}
	// OpenFGA returns fully-qualified "<type>:<id>"; strip the type prefix.
	// Guard the prefix: an entry that isn't of the requested type (or is
	// malformed/empty) must be skipped, never sliced blindly — otherwise a
	// short entry panics and a wrong-type entry yields a corrupt id.
	prefix := objectType + ":"
	ids := make([]string, 0, len(out.Objects))
	for _, o := range out.Objects {
		if !strings.HasPrefix(o, prefix) {
			continue
		}
		ids = append(ids, o[len(prefix):])
	}
	return ids, nil
}

func tuple(user pii.Subject, relation, object string) map[string]any {
	return rawTuple(user.OpenFGAObject(), relation, object)
}

func rawTuple(user, relation, object string) map[string]any {
	return map[string]any{
		"user":     user,
		"relation": relation,
		"object":   object,
	}
}
