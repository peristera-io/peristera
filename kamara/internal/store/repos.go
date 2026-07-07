package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/peristera-io/peristera/kamara/internal/engine"
	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/wopi"
	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/dbtx"
	"github.com/peristera-io/peristera/lib/pgconv"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// ObjectRepo is the Postgres implementation of file.Repo over a
// dbtx.Executor (root ADR-0015).
type ObjectRepo struct{ db dbtx.Executor }

const objCols = `id, owner_instance, owner_user, name, size, folder_id, content_type, created_at, updated_at`

func nullToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func scanObject(row interface{ Scan(...any) error }) (file.Object, error) {
	var o file.Object
	var folder sql.NullString
	err := row.Scan(&o.ID, &o.Owner.Instance, &o.Owner.UserID, &o.Name, &o.Size, &folder, &o.ContentType, &o.Created, &o.Updated)
	o.FolderID = nullToPtr(folder)
	return o, err
}

func (r *ObjectRepo) InsertObject(ctx context.Context, o file.Object) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO objects (`+objCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		o.ID, o.Owner.Instance, o.Owner.UserID, o.Name, o.Size, o.FolderID, o.ContentType, o.Created, o.Updated)
	return err
}

func (r *ObjectRepo) SetObjectFolder(ctx context.Context, id string, folderID *string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE objects SET folder_id=$2, updated_at=now() WHERE id=$1`, id, folderID)
	return err
}

func (r *ObjectRepo) SetObjectName(ctx context.Context, id, name string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE objects SET name=$2, updated_at=now() WHERE id=$1`, id, name)
	return err
}

// SetObjectSize records the latest version's size on the object and bumps
// updated_at (called on WOPI save-back — ADR-0018).
func (r *ObjectRepo) SetObjectSize(ctx context.Context, id string, size int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE objects SET size=$2, updated_at=now() WHERE id=$1`, id, size)
	return err
}

func (r *ObjectRepo) GetObject(ctx context.Context, id string) (file.Object, bool, error) {
	o, err := scanObject(r.db.QueryRowContext(ctx, `SELECT `+objCols+` FROM objects WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return file.Object{}, false, nil
	}
	return o, err == nil, err
}

func (r *ObjectRepo) ByIDs(ctx context.Context, ids []string) ([]file.Object, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+objCols+` FROM objects WHERE id = ANY($1) ORDER BY created_at`, ids)
	if err != nil {
		return nil, err
	}
	return collectObjects(rows)
}

func (r *ObjectRepo) ByOwner(ctx context.Context, o pii.Subject) ([]file.Object, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+objCols+` FROM objects WHERE owner_instance=$1 AND owner_user=$2 ORDER BY created_at`,
		o.Instance, o.UserID)
	if err != nil {
		return nil, err
	}
	return collectObjects(rows)
}

func collectObjects(rows *sql.Rows) ([]file.Object, error) {
	defer rows.Close()
	var out []file.Object
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *ObjectRepo) DeleteObject(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM objects WHERE id=$1`, id) // cascades versions + version_chunks
	return err
}

// InsertVersion records a version and its manifest: the version row, each
// version_chunks entry, and an upsert-with-increment on the chunk (dedup +
// ref-counting).
func (r *ObjectRepo) InsertVersion(ctx context.Context, objectID, versionID string, ordinal int, size int64, refs []engine.ChunkRef) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO versions (id, object_id, ordinal, size, created_at) VALUES ($1,$2,$3,$4,now())`,
		versionID, objectID, ordinal, size); err != nil {
		// A concurrent save took this ordinal (unique on object_id, ordinal);
		// surface it as a domain conflict so the service can retry.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return file.ErrVersionConflict
		}
		return err
	}
	for i, ref := range refs {
		// Upsert the chunk, incrementing ref_count on an existing hash.
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO chunks (hash, size, ref_count) VALUES ($1,$2,1)
			 ON CONFLICT (hash) DO UPDATE SET ref_count = chunks.ref_count + 1`,
			ref.Hash, ref.Size); err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO version_chunks (version_id, idx, chunk_hash) VALUES ($1,$2,$3)`,
			versionID, i, ref.Hash); err != nil {
			return err
		}
	}
	return nil
}

// ManifestOf returns the ordered chunk refs of an object's latest version.
func (r *ObjectRepo) ManifestOf(ctx context.Context, objectID string) ([]engine.ChunkRef, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT vc.chunk_hash, c.size
		   FROM version_chunks vc JOIN chunks c ON c.hash = vc.chunk_hash
		  WHERE vc.version_id = (SELECT id FROM versions WHERE object_id=$1 ORDER BY ordinal DESC LIMIT 1)
		  ORDER BY vc.idx`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []engine.ChunkRef
	for rows.Next() {
		var ref engine.ChunkRef
		if err := rows.Scan(&ref.Hash, &ref.Size); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// MaxOrdinal returns the highest version ordinal of an object; ok is false
// when the object has no versions (MAX over no rows is SQL NULL).
func (r *ObjectRepo) MaxOrdinal(ctx context.Context, objectID string) (int, bool, error) {
	var ord sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		`SELECT MAX(ordinal) FROM versions WHERE object_id=$1`, objectID).Scan(&ord)
	if err != nil {
		return 0, false, err
	}
	return int(ord.Int64), ord.Valid, nil
}

// ListVersions returns an object's versions, newest first.
func (r *ObjectRepo) ListVersions(ctx context.Context, objectID string) ([]file.Version, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, ordinal, size, created_at FROM versions WHERE object_id=$1 ORDER BY ordinal DESC`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vs []file.Version
	for rows.Next() {
		var v file.Version
		if err := rows.Scan(&v.ID, &v.Ordinal, &v.Size, &v.Created); err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}

// ChunkHashesOf returns every chunk hash the object references (across all
// its versions), one entry per manifest reference (duplicates preserved so
// ref-count decrements match the increments).
//
// WHOLE-OBJECT-DELETE ONLY: it is object-scoped, not version-scoped, so it
// is symmetric with the sum of all InsertVersion increments for the object.
// When per-version reclaim arrives (versioning session), it needs a
// version-scoped query instead — this one would over-decrement.
func (r *ObjectRepo) ChunkHashesOf(ctx context.Context, objectID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT vc.chunk_hash FROM version_chunks vc
		   JOIN versions v ON v.id = vc.version_id WHERE v.object_id=$1`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hs []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		hs = append(hs, h)
	}
	return hs, rows.Err()
}

// DecRef decrements ref_count once per occurrence in hashes (aggregated so
// a chunk appearing twice in a manifest is decremented twice), and returns
// the hashes that reached zero.
func (r *ObjectRepo) DecRef(ctx context.Context, hashes []string) ([]string, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	counts := map[string]int{}
	for _, h := range hashes {
		counts[h]++
	}
	distinct := make([]string, 0, len(counts))
	for h, n := range counts {
		if _, err := r.db.ExecContext(ctx,
			`UPDATE chunks SET ref_count = ref_count - $2 WHERE hash=$1`, h, n); err != nil {
			return nil, err
		}
		distinct = append(distinct, h)
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT hash FROM chunks WHERE hash = ANY($1) AND ref_count <= 0`, distinct)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orphans []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		orphans = append(orphans, h)
	}
	return orphans, rows.Err()
}

func (r *ObjectRepo) DeleteChunks(ctx context.Context, hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM chunks WHERE hash = ANY($1)`, hashes)
	return err
}

// --- folders (Kamara ADR-0002) ---

const folderCols = `id, owner_instance, owner_user, parent_id, name, created_at, updated_at`

func scanFolder(row interface{ Scan(...any) error }) (file.Folder, error) {
	var f file.Folder
	var parent sql.NullString
	err := row.Scan(&f.ID, &f.Owner.Instance, &f.Owner.UserID, &parent, &f.Name, &f.Created, &f.Updated)
	f.ParentID = nullToPtr(parent)
	return f, err
}

func (r *ObjectRepo) InsertFolder(ctx context.Context, f file.Folder) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO folders (`+folderCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		f.ID, f.Owner.Instance, f.Owner.UserID, f.ParentID, f.Name, f.Created, f.Updated)
	return err
}

func (r *ObjectRepo) GetFolder(ctx context.Context, id string) (file.Folder, bool, error) {
	f, err := scanFolder(r.db.QueryRowContext(ctx, `SELECT `+folderCols+` FROM folders WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return file.Folder{}, false, nil
	}
	return f, err == nil, err
}

func (r *ObjectRepo) FoldersInParent(ctx context.Context, owner pii.Subject, parent *string) ([]file.Folder, error) {
	// IS NOT DISTINCT FROM matches a NULL parent (root) as well as a value.
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+folderCols+` FROM folders
		  WHERE owner_instance=$1 AND owner_user=$2 AND parent_id IS NOT DISTINCT FROM $3::text
		  ORDER BY name`, owner.Instance, owner.UserID, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []file.Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *ObjectRepo) ObjectsInFolder(ctx context.Context, owner pii.Subject, folder *string) ([]file.Object, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+objCols+` FROM objects
		  WHERE owner_instance=$1 AND owner_user=$2 AND folder_id IS NOT DISTINCT FROM $3::text
		  ORDER BY name`, owner.Instance, owner.UserID, folder)
	if err != nil {
		return nil, err
	}
	return collectObjects(rows)
}

func (r *ObjectRepo) FolderHasChildren(ctx context.Context, id string) (bool, error) {
	var has bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM folders WHERE parent_id=$1)
		     OR EXISTS(SELECT 1 FROM objects WHERE folder_id=$1)`, id).Scan(&has)
	return has, err
}

func (r *ObjectRepo) SetFolderParent(ctx context.Context, id string, parent *string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE folders SET parent_id=$2, updated_at=now() WHERE id=$1`, id, parent)
	return err
}

func (r *ObjectRepo) SetFolderName(ctx context.Context, id, name string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE folders SET name=$2, updated_at=now() WHERE id=$1`, id, name)
	return err
}

func (r *ObjectRepo) DeleteFolder(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM folders WHERE id=$1`, id)
	// A child added between the service's empty-check and here trips the
	// parent_id / folder_id ON DELETE RESTRICT (SQLSTATE 23503) — surface
	// that race as "not empty" (409), not an opaque 500 (#36).
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return file.ErrNotEmpty
	}
	return err
}

func (r *ObjectRepo) FoldersByOwner(ctx context.Context, owner pii.Subject) ([]file.Folder, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+folderCols+` FROM folders WHERE owner_instance=$1 AND owner_user=$2 ORDER BY created_at`,
		owner.Instance, owner.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []file.Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

var _ file.Repo = (*ObjectRepo)(nil)

// --- unit of work (root ADR-0015) ---

func (d *DB) storesFor(e dbtx.Executor) file.Stores {
	return file.Stores{
		Objects: &ObjectRepo{db: e},
		Audit:   audit.NewEmitter(&pgconv.AuditSink{DB: e}, pii.NewPseudonyms(&pgconv.PseudonymStore{DB: e})),
		Search:  search.NewFeeder(&pgconv.SearchIndex{DB: e}),
	}
}

// Reader returns a non-transactional store bundle (reads, export/erase).
func (d *DB) Reader() file.Stores { return d.storesFor(d.sql) }

// WopiSessions returns the WOPI session store (ADR-0018). Sessions live
// outside the file unit of work — minted on page render, read on each WOPI
// call — so they use the base (non-transactional) executor.
func (d *DB) WopiSessions() *WopiRepo { return &WopiRepo{db: d.sql} }

// WopiRepo persists WOPI editing sessions (wopi.Store).
type WopiRepo struct{ db dbtx.Executor }

func (r *WopiRepo) Put(ctx context.Context, tokenHash string, s wopi.Session) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO wopi_sessions (token_hash, object_id, subject_instance, subject_user, can_write, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (token_hash) DO NOTHING`,
		tokenHash, s.ObjectID, s.Subject.Instance, s.Subject.UserID, s.CanWrite, s.Expires)
	return err
}

func (r *WopiRepo) Get(ctx context.Context, tokenHash string) (wopi.Session, bool, error) {
	var s wopi.Session
	err := r.db.QueryRowContext(ctx,
		`SELECT object_id, subject_instance, subject_user, can_write, expires_at
		   FROM wopi_sessions WHERE token_hash=$1`, tokenHash).
		Scan(&s.ObjectID, &s.Subject.Instance, &s.Subject.UserID, &s.CanWrite, &s.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return wopi.Session{}, false, nil
	}
	return s, err == nil, err
}

func (r *WopiRepo) DeleteExpired(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM wopi_sessions WHERE expires_at < $1`, before)
	return err
}

func (r *WopiRepo) DeleteByObject(ctx context.Context, objectID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM wopi_sessions WHERE object_id=$1`, objectID)
	return err
}

var _ wopi.Store = (*WopiRepo)(nil)

// InTx runs fn with a transaction-bound store bundle, atomically.
func (d *DB) InTx(ctx context.Context, fn func(file.Stores) error) error {
	return dbtx.InTx(ctx, d.sql, func(tx *sql.Tx) error { return fn(d.storesFor(tx)) })
}

var _ file.TxRunner = (*DB)(nil)
