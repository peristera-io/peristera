package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/peristera-io/peristera/kamara/internal/engine"
	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/dbtx"
	"github.com/peristera-io/peristera/lib/pgconv"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// ObjectRepo is the Postgres implementation of file.Repo over a
// dbtx.Executor (root ADR-0015).
type ObjectRepo struct{ db dbtx.Executor }

const objCols = `id, owner_instance, owner_user, name, size, created_at, updated_at`

func scanObject(row interface{ Scan(...any) error }) (file.Object, error) {
	var o file.Object
	err := row.Scan(&o.ID, &o.Owner.Instance, &o.Owner.UserID, &o.Name, &o.Size, &o.Created, &o.Updated)
	return o, err
}

func (r *ObjectRepo) InsertObject(ctx context.Context, o file.Object) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO objects (`+objCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		o.ID, o.Owner.Instance, o.Owner.UserID, o.Name, o.Size, o.Created, o.Updated)
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

// ChunkHashesOf returns every chunk hash the object references (across all
// its versions), one entry per manifest reference (duplicates preserved so
// ref-count decrements match the increments).
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

// InTx runs fn with a transaction-bound store bundle, atomically.
func (d *DB) InTx(ctx context.Context, fn func(file.Stores) error) error {
	return dbtx.InTx(ctx, d.sql, func(tx *sql.Tx) error { return fn(d.storesFor(tx)) })
}

var _ file.TxRunner = (*DB)(nil)
