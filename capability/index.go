package capability

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/mattn/go-sqlite3"
)

type EmbedFunc func(context.Context, string) ([]float32, error)

type Embedder struct {
	Model string
	Embed EmbedFunc
}

type Match struct {
	Capability Capability `json:"capability"`
	Distance   float64    `json:"distance"`
}

type Index struct {
	db        *sql.DB
	embedder  Embedder
	dimension int
}

func Rebuild(ctx context.Context, path, extensionPath string, registry Registry, embedder Embedder) error {
	if len(registry.Capabilities) == 0 {
		return errors.New("registry has no capabilities")
	}
	if err := validateEmbedder(embedder); err != nil {
		return err
	}

	vectors := make([][]float32, len(registry.Capabilities))
	dimension := 0
	for i, capability := range registry.Capabilities {
		vector, err := embedder.Embed(ctx, capability.EmbeddingText())
		if err != nil {
			return fmt.Errorf("embed %s: %w", capability.Name, err)
		}
		if len(vector) == 0 {
			return fmt.Errorf("embed %s: empty vector", capability.Name)
		}
		if dimension == 0 {
			dimension = len(vector)
		} else if len(vector) != dimension {
			return fmt.Errorf("embed %s: got dimension %d, want %d", capability.Name, len(vector), dimension)
		}
		vectors[i] = vector
	}

	db, err := openVectorDB(ctx, path, extensionPath)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
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
	if _, err := tx.ExecContext(ctx,
		`SELECT vector_init('capabilities', 'embedding', ?)`,
		fmt.Sprintf("type=FLOAT32,dimension=%d,distance=COSINE", dimension),
	); err != nil {
		return fmt.Errorf("initialize vector column: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO capability_index_meta(dimension, embedding_model) VALUES (?, ?)`, dimension, embedder.Model); err != nil {
		return err
	}

	for i, capability := range registry.Capabilities {
		input, err := json.Marshal(capability.Input)
		if err != nil {
			return err
		}
		output, err := json.Marshal(capability.Output)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO capabilities(name, rpc, description, input_json, output_json, embedding)
			VALUES (?, ?, ?, ?, ?, vector_as_f32(?, ?))
		`, capability.Name, capability.RPC, capability.Description, input, output, encodeVector(vectors[i]), dimension); err != nil {
			return fmt.Errorf("index %s: %w", capability.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit capability index: %w", err)
	}
	return nil
}

func Open(ctx context.Context, path, extensionPath string, embedder Embedder) (*Index, error) {
	if err := validateEmbedder(embedder); err != nil {
		return nil, err
	}
	db, err := openVectorDB(ctx, path, extensionPath)
	if err != nil {
		return nil, err
	}

	var dimension int
	var model string
	if err := db.QueryRowContext(ctx, `SELECT dimension, embedding_model FROM capability_index_meta`).Scan(&dimension, &model); err != nil {
		db.Close()
		return nil, fmt.Errorf("read capability index metadata: %w", err)
	}
	if model != embedder.Model {
		db.Close()
		return nil, fmt.Errorf("capability index uses embedding model %q, not %q", model, embedder.Model)
	}
	if _, err := db.ExecContext(ctx,
		`SELECT vector_init('capabilities', 'embedding', ?)`,
		fmt.Sprintf("type=FLOAT32,dimension=%d,distance=COSINE", dimension),
	); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize vector column: %w", err)
	}

	return &Index{db: db, embedder: embedder, dimension: dimension}, nil
}

func (index *Index) Close() error {
	return index.db.Close()
}

func (index *Index) Search(ctx context.Context, query string, limit int) ([]Match, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit < 1 {
		return nil, errors.New("limit must be positive")
	}

	vector, err := index.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vector) != index.dimension {
		return nil, fmt.Errorf("query embedding has dimension %d, want %d", len(vector), index.dimension)
	}

	rows, err := index.db.QueryContext(ctx, `
		SELECT c.name, c.rpc, c.description, c.input_json, c.output_json, v.distance
		FROM vector_full_scan('capabilities', 'embedding', ?, ?) AS v
		JOIN capabilities AS c ON c.rowid = v.rowid
		ORDER BY v.distance
	`, encodeVector(vector), limit)
	if err != nil {
		return nil, fmt.Errorf("search capabilities: %w", err)
	}
	defer rows.Close()

	matches := make([]Match, 0, limit)
	for rows.Next() {
		var match Match
		var input, output []byte
		if err := rows.Scan(
			&match.Capability.Name,
			&match.Capability.RPC,
			&match.Capability.Description,
			&input,
			&output,
			&match.Distance,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(input, &match.Capability.Input); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(output, &match.Capability.Output); err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	return matches, rows.Err()
}

func openVectorDB(ctx context.Context, path, extensionPath string) (*sql.DB, error) {
	if extensionPath == "" {
		return nil, errors.New("sqlite-vector extension path is required")
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
	return db, nil
}

func validateEmbedder(embedder Embedder) error {
	if strings.TrimSpace(embedder.Model) == "" {
		return errors.New("embedding model is required")
	}
	if embedder.Embed == nil {
		return errors.New("embed function is required")
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
