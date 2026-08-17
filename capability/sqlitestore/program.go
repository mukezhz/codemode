package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/program"
)

// programTable holds cached programs beside the capability index, in the same
// database file and on the same connection. A deployment that has one has both.
const programTable = "cached_programs"

// OpenProgramCache returns the program store held in the same SQLite database
// as store, which must be one this package opened. Dimension must match the
// embedding model the cache will be used with — capability.Store.Metadata
// reports the one the capability index was built with, and using it here keeps
// the two consistent.
//
// Wrap the result with program.New to get a program.Cache. Closing that closes
// this, which does not close the capability store: they share a connection, and
// the capability store owns it.
func OpenProgramCache(ctx context.Context, store capability.Store, dimension int) (program.Store, error) {
	backing, ok := store.(*sqliteStore)
	if !ok {
		return nil, errors.New("program cache needs a store opened by capability/sqlitestore")
	}
	if dimension < 1 {
		return nil, errors.New("embedding dimension is required")
	}
	cache := &sqliteProgramStore{db: backing.db, dimension: dimension}
	if err := cache.migrate(ctx); err != nil {
		return nil, err
	}
	return cache, nil
}

type sqliteProgramStore struct {
	db        *sql.DB
	dimension int
}

func (store *sqliteProgramStore) migrate(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+programTable+` (
			id INTEGER PRIMARY KEY,
			scope TEXT NOT NULL,
			request TEXT NOT NULL,
			source TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			embedding BLOB NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS cached_programs_request
			ON `+programTable+`(scope, request);
	`); err != nil {
		return fmt.Errorf("create program cache: %w", err)
	}
	return store.initVector(ctx)
}

// initVector re-declares the vector column, which the extension requires on
// every connection before the column can be queried.
func (store *sqliteProgramStore) initVector(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx,
		`SELECT vector_init('`+programTable+`', 'embedding', ?)`,
		fmt.Sprintf("type=FLOAT32,dimension=%d,distance=COSINE", store.dimension),
	); err != nil {
		return fmt.Errorf("initialize program cache vector column: %w", err)
	}
	return nil
}

func (store *sqliteProgramStore) Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]program.Hit, error) {
	// The scan is filtered to the scope before ranking, so a program written
	// for another dialect, capability set, or caller can never be returned.
	rows, err := store.db.QueryContext(ctx, `
		SELECT p.id, p.source, v.distance
		FROM vector_full_scan('cached_programs', 'embedding', ?, ?) AS v
		JOIN `+programTable+` AS p ON p.rowid = v.rowid
		WHERE p.scope = ?
		ORDER BY v.distance
		LIMIT ?
	`, encodeVector(vector), scanBudget(limit), scope, limit)
	if err != nil {
		return nil, fmt.Errorf("search program cache: %w", err)
	}
	defer rows.Close()
	return scanHits(rows, limit)
}

// scanBudget widens the vector scan before the scope filter narrows it. The
// extension ranks across every scope, so asking it for exactly limit rows would
// return nothing whenever the closest programs belong to other scopes.
//
// ponytail: a fixed multiplier, not a loop that widens until it fills. A miss
// costs a model call the request was going to make anyway.
func scanBudget(limit int) int { return limit * 64 }

func (store *sqliteProgramStore) Put(ctx context.Context, record program.Record) error {
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO `+programTable+`(scope, request, source, created_at, embedding)
		VALUES (?, ?, ?, unixepoch(), vector_as_f32(?, ?))
		ON CONFLICT(scope, request) DO UPDATE SET
			source = excluded.source,
			created_at = excluded.created_at,
			embedding = excluded.embedding
	`, record.Scope, record.Request, record.Source,
		encodeVector(record.Vector), store.dimension); err != nil {
		return fmt.Errorf("cache program: %w", err)
	}
	return nil
}

func (store *sqliteProgramStore) Delete(ctx context.Context, id string) error {
	rowID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("program cache id %q: %w", id, err)
	}
	if _, err := store.db.ExecContext(ctx,
		`DELETE FROM `+programTable+` WHERE id = ?`, rowID); err != nil {
		return fmt.Errorf("forget program: %w", err)
	}
	return nil
}

// Close is a no-op: the connection belongs to the capability store this cache
// was opened from, and closing it here would close that too.
func (store *sqliteProgramStore) Close() error { return nil }

// scanHits decodes cached program rows in the column order both SQL backends
// select: id, source, distance.
func scanHits(rows *sql.Rows, limit int) ([]program.Hit, error) {
	hits := make([]program.Hit, 0, limit)
	for rows.Next() {
		var hit program.Hit
		if err := rows.Scan(&hit.ID, &hit.Source, &hit.Distance); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}
