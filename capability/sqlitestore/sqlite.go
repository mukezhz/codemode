// Package sqlitestore indexes capabilities in SQLite using the sqlite-vector
// extension.
//
// Blank-import it to make CAPABILITY_DB=sqlite available:
//
//	import _ "github.com/mukezhz/codemode/capability/sqlitestore"
//
// It requires cgo, because the SQLite driver and the vector extension are C.
// A binary that must build with CGO_ENABLED=0 should use capability/pgstore
// instead, or no store at all.
package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/mattn/go-sqlite3"

	"github.com/mukezhz/codemode/capability"
)

// sqliteStore indexes capabilities in SQLite using the sqlite-vector extension.
type sqliteStore struct {
	db *sql.DB
}

// Open opens the SQLite index at path, loading the sqlite-vector library from
// extensionPath. Ctx carries cancellation. The caller closes the store.
//
// It reads no environment variable. capability.OpenStoreFromEnv reaches this
// through OpenFromEnv, which supplies extensionPath from
// SQLITE_VECTOR_EXTENSION.
func Open(ctx context.Context, path, extensionPath string) (capability.Store, error) {
	if extensionPath == "" {
		return nil, errors.New("sqlite-vector extension path is required (SQLITE_VECTOR_EXTENSION)")
	}
	if _, err := os.Stat(extensionPath); err != nil {
		return nil, fmt.Errorf("sqlite-vector extension: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	// ponytail: one connection keeps extension initialization simple; register a
	// per-connection driver hook if search throughput ever needs a pool.
	db.SetMaxOpenConns(1)

	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	err = conn.Raw(func(driverConn any) error {
		sqliteConn, ok := driverConn.(*sqlite3.SQLiteConn)
		if !ok {
			return errors.New("unexpected SQLite driver")
		}
		return sqliteConn.LoadExtension(extensionPath, "sqlite3_vector_init")
	})
	conn.Close()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("load sqlite-vector extension: %w", err)
	}
	return &sqliteStore{db: db}, nil
}

func (store *sqliteStore) Close() error { return store.db.Close() }

func (store *sqliteStore) Rebuild(ctx context.Context, model string, dimension int, records []capability.Record) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DROP TABLE IF EXISTS capabilities;
		DROP TABLE IF EXISTS capability_index_meta;
		CREATE TABLE capabilities (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			rpc TEXT NOT NULL,
			description TEXT NOT NULL,
			input_json TEXT NOT NULL,
			output_json TEXT NOT NULL,
			embedding BLOB NOT NULL
		);
		CREATE TABLE capability_index_meta (
			dimension INTEGER NOT NULL,
			embedding_model TEXT NOT NULL
		);
	`); err != nil {
		return fmt.Errorf("create capability index: %w", err)
	}
	if err := sqliteInitVector(ctx, tx, dimension); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO capability_index_meta(dimension, embedding_model) VALUES (?, ?)`,
		dimension, model,
	); err != nil {
		return err
	}

	for _, record := range records {
		input, output, err := capability.EncodeSchemas(record.Capability)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO capabilities(name, rpc, description, input_json, output_json, embedding)
			VALUES (?, ?, ?, ?, ?, vector_as_f32(?, ?))
		`, record.Capability.Name, record.Capability.RPC, record.Capability.Description,
			input, output, encodeVector(record.Vector), dimension); err != nil {
			return fmt.Errorf("index %s: %w", record.Capability.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit capability index: %w", err)
	}
	return nil
}

func (store *sqliteStore) Metadata(ctx context.Context) (string, int, error) {
	var model string
	var dimension int
	if err := store.db.QueryRowContext(ctx,
		`SELECT embedding_model, dimension FROM capability_index_meta`,
	).Scan(&model, &dimension); err != nil {
		return "", 0, fmt.Errorf("read capability index metadata: %w", err)
	}
	// The extension needs the vector column re-declared on each connection
	// before it can be queried.
	if err := sqliteInitVector(ctx, store.db, dimension); err != nil {
		return "", 0, err
	}
	return model, dimension, nil
}

func (store *sqliteStore) Search(ctx context.Context, vector []float32, limit int) ([]capability.Match, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT c.name, c.rpc, c.description, c.input_json, c.output_json, v.distance
		FROM vector_full_scan('capabilities', 'embedding', ?, ?) AS v
		JOIN capabilities AS c ON c.rowid = v.rowid
		ORDER BY v.distance
	`, encodeVector(vector), limit)
	if err != nil {
		return nil, fmt.Errorf("search capabilities: %w", err)
	}
	defer rows.Close()
	return capability.ScanMatches(rows, limit)
}

// execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func sqliteInitVector(ctx context.Context, db execer, dimension int) error {
	if _, err := db.ExecContext(ctx,
		`SELECT vector_init('capabilities', 'embedding', ?)`,
		fmt.Sprintf("type=FLOAT32,dimension=%d,distance=COSINE", dimension),
	); err != nil {
		return fmt.Errorf("initialize vector column: %w", err)
	}
	return nil
}

func encodeVector(vector []float32) []byte {
	encoded := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(encoded[i*4:], math.Float32bits(value))
	}
	return encoded
}
