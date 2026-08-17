package pgstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/program"
)

// OpenProgramCache returns the program store held in the same database as
// store, which must be one this package opened. Dimension must match the
// embedding model the cache will be used with — capability.Store.Metadata
// reports the one the capability index was built with, and using it here keeps
// the two consistent.
//
// Wrap the result with program.New to get a program.Cache. Closing that closes
// this, which does not close the capability store: they share a connection pool,
// and the capability store owns it.
func OpenProgramCache(ctx context.Context, store capability.Store, dimension int) (program.Store, error) {
	backing, ok := store.(*pgStore)
	if !ok {
		return nil, errors.New("program cache needs a store opened by capability/pgstore")
	}
	if dimension < 1 {
		return nil, errors.New("embedding dimension is required")
	}
	cache := &pgProgramStore{db: backing.db}
	if err := cache.migrate(ctx, dimension); err != nil {
		return nil, err
	}
	return cache, nil
}

type pgProgramStore struct {
	db *sql.DB
}

func (store *pgProgramStore) migrate(ctx context.Context, dimension int) error {
	// The vector column is typed by dimension, so a cache built for one
	// embedding model cannot hold vectors from another. Rather than silently
	// mixing them, an existing table at a different dimension is replaced: the
	// cache is derived data, and rebuilding it costs one model call per
	// request that comes back.
	var existing int
	err := store.db.QueryRowContext(ctx, `
		SELECT a.atttypmod
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relname = 'cached_programs' AND a.attname = 'embedding'
	`).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No table yet.
	case err != nil:
		return fmt.Errorf("inspect program cache: %w", err)
	case existing != dimension:
		if _, err := store.db.ExecContext(ctx, `DROP TABLE cached_programs`); err != nil {
			return fmt.Errorf("drop program cache at old dimension: %w", err)
		}
	}

	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS cached_programs (
			id BIGSERIAL PRIMARY KEY,
			scope TEXT NOT NULL,
			request TEXT NOT NULL,
			source TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			embedding vector(%d) NOT NULL,
			UNIQUE (scope, request)
		);
		CREATE INDEX IF NOT EXISTS cached_programs_scope ON cached_programs (scope);
	`, dimension)); err != nil {
		return fmt.Errorf("create program cache: %w", err)
	}
	return nil
}

func (store *pgProgramStore) Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]program.Hit, error) {
	// <=> is pgvector's cosine distance operator, matching the capability
	// index. The scope filter is part of the query rather than applied after,
	// so a program written for another dialect, capability set, or caller can
	// never be returned.
	rows, err := store.db.QueryContext(ctx, `
		SELECT id::text, source, embedding <=> $1 AS distance
		FROM cached_programs
		WHERE scope = $2
		ORDER BY distance
		LIMIT $3
	`, formatVector(vector), scope, limit)
	if err != nil {
		return nil, fmt.Errorf("search program cache: %w", err)
	}
	defer rows.Close()

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

func (store *pgProgramStore) Put(ctx context.Context, record program.Record) error {
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO cached_programs (scope, request, source, embedding)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (scope, request) DO UPDATE SET
			source = EXCLUDED.source,
			created_at = now(),
			embedding = EXCLUDED.embedding
	`, record.Scope, record.Request, record.Source, formatVector(record.Vector)); err != nil {
		return fmt.Errorf("cache program: %w", err)
	}
	return nil
}

func (store *pgProgramStore) Delete(ctx context.Context, id string) error {
	if _, err := store.db.ExecContext(ctx,
		`DELETE FROM cached_programs WHERE id = $1::bigint`, id); err != nil {
		return fmt.Errorf("forget program: %w", err)
	}
	return nil
}

// Close is a no-op: the connection pool belongs to the capability store this
// cache was opened from, and closing it here would close that too.
func (store *pgProgramStore) Close() error { return nil }
