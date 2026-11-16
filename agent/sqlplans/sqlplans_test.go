package sqlplans_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/agent/plantest"
	"github.com/mukezhz/codemode/agent/sqlplans"
)

func TestSQLitePassesTheContract(t *testing.T) {
	plantest.Run(t, func(t *testing.T) agent.PlanStore {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1) // every connection to :memory: is its own database
		t.Cleanup(func() { db.Close() })
		store, err := sqlplans.Open(context.Background(), db, sqlplans.SQLite)
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}

func TestAPlanSurvivesReopening(t *testing.T) {
	path := t.TempDir() + "/plans.db"
	ctx := context.Background()
	open := func() (*sql.DB, agent.PlanStore) {
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatal(err)
		}
		store, err := sqlplans.Open(ctx, db, sqlplans.SQLite)
		if err != nil {
			t.Fatal(err)
		}
		return db, store
	}
	db, store := open()
	if err := store.Create(ctx, agent.Plan{ID: "p1", Owner: "ada", Status: agent.PlanPending}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, store = open()
	defer db.Close()
	if plan, err := store.Get(ctx, "ada", "p1"); err != nil || plan.Status != agent.PlanPending {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
}

// make test-postgres sets the DSN; without it the test is skipped.
func TestPostgresPassesTheContract(t *testing.T) {
	dsn := os.Getenv("CAPABILITY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CAPABILITY_TEST_POSTGRES_DSN is not set")
	}
	plantest.Run(t, func(t *testing.T) agent.PlanStore {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if _, err := db.Exec(`DROP TABLE IF EXISTS codemode_plans`); err != nil {
			t.Fatal(err)
		}
		store, err := sqlplans.Open(context.Background(), db, sqlplans.Postgres)
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}
