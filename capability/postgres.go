package capability

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// postgresStore indexes capabilities in PostgreSQL using the pgvector
// extension. The database must allow CREATE EXTENSION vector, or have it
// installed already.
type postgresStore struct {
	db *sql.DB
}

// OpenPostgres connects to the Postgres index named by dsn, a libpq connection
// string or URL such as postgres://user:pass@host:5432/db. Ctx carries
// cancellation. The caller closes the store.
func OpenPostgres(ctx context.Context, dsn string) (Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable pgvector (install it, or grant CREATE on the database): %w", err)
	}
	return &postgresStore{db: db}, nil
}

func (store *postgresStore) Close() error { return store.db.Close() }

func (store *postgresStore) Rebuild(ctx context.Context, model string, dimension int, records []Record) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// The vector column is typed by dimension, so a rebuild at a new dimension
	// has to recreate the table rather than truncate it.
	if _, err := tx.ExecContext(ctx, `
		DROP TABLE IF EXISTS capabilities;
		DROP TABLE IF EXISTS capability_index_meta;
	`); err != nil {
		return fmt.Errorf("drop capability index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE capabilities (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			rpc TEXT NOT NULL,
			description TEXT NOT NULL,
			input_json TEXT NOT NULL,
			output_json TEXT NOT NULL,
			embedding vector(%d) NOT NULL
		);
		CREATE TABLE capability_index_meta (
			dimension INTEGER NOT NULL,
			embedding_model TEXT NOT NULL
		);
	`, dimension)); err != nil {
		return fmt.Errorf("create capability index: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO capability_index_meta(dimension, embedding_model) VALUES ($1, $2)`,
		dimension, model,
	); err != nil {
		return err
	}

	for _, record := range records {
		input, output, err := encodeSchemas(record.Capability)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO capabilities(name, rpc, description, input_json, output_json, embedding)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, record.Capability.Name, record.Capability.RPC, record.Capability.Description,
			string(input), string(output), formatVector(record.Vector)); err != nil {
			return fmt.Errorf("index %s: %w", record.Capability.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit capability index: %w", err)
	}
	return nil
}

func (store *postgresStore) Metadata(ctx context.Context) (string, int, error) {
	var model string
	var dimension int
	if err := store.db.QueryRowContext(ctx,
		`SELECT embedding_model, dimension FROM capability_index_meta`,
	).Scan(&model, &dimension); err != nil {
		return "", 0, fmt.Errorf("read capability index metadata: %w", err)
	}
	return model, dimension, nil
}

func (store *postgresStore) Search(ctx context.Context, vector []float32, limit int) ([]Match, error) {
	// <=> is pgvector's cosine distance operator, matching the SQLite backend.
	rows, err := store.db.QueryContext(ctx, `
		SELECT name, rpc, description, input_json, output_json, embedding <=> $1 AS distance
		FROM capabilities
		ORDER BY distance
		LIMIT $2
	`, formatVector(vector), limit)
	if err != nil {
		return nil, fmt.Errorf("search capabilities: %w", err)
	}
	defer rows.Close()
	return scanMatches(rows, limit)
}

// formatVector renders a vector in pgvector's text form, "[1,2,3]".
func formatVector(vector []float32) string {
	var builder strings.Builder
	builder.WriteByte('[')
	for i, value := range vector {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(float64(value), 'g', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String()
}
