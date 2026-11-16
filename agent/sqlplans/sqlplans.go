// Package sqlplans keeps agent plans in SQLite or Postgres through database/sql.
// It imports no driver: open the database with your own and pass it in.
package sqlplans

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mukezhz/codemode/agent"
)

type Dialect int

const (
	SQLite Dialect = iota
	Postgres
)

const table = "codemode_plans"

type store struct {
	db      *sql.DB
	dialect Dialect
}

var _ agent.PlanStore = (*store)(nil)

// Open creates the codemode_plans table if it is missing. The table can share a
// database with your application's own.
func Open(ctx context.Context, db *sql.DB, dialect Dialect) (agent.PlanStore, error) {
	if db == nil {
		return nil, errors.New("sqlplans: db is nil")
	}
	plans := &store{db: db, dialect: dialect}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS ` + table + ` (
			id TEXT PRIMARY KEY,
			owner TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			version INTEGER NOT NULL,
			body TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS ` + table + `_owner ON ` + table + ` (owner, status, created_at)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("sqlplans: create table: %w", err)
		}
	}
	return plans, nil
}

// bind rewrites ? placeholders as $1, $2, ... for Postgres.
func (plans *store) bind(query string) string {
	if plans.dialect != Postgres {
		return query
	}
	var builder strings.Builder
	number := 0
	for _, r := range query {
		if r == '?' {
			number++
			builder.WriteString("$" + strconv.Itoa(number))
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func (plans *store) Create(ctx context.Context, plan agent.Plan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = plans.db.ExecContext(ctx, plans.bind(
		`INSERT INTO `+table+` (id, owner, status, created_at, version, body) VALUES (?, ?, ?, ?, ?, ?)`),
		plan.ID, plan.Owner, string(plan.Status), plan.CreatedAt.UnixNano(), plan.Version, string(body))
	if err != nil {
		return fmt.Errorf("sqlplans: create plan %s: %w", plan.ID, err)
	}
	return nil
}

func (plans *store) Get(ctx context.Context, owner, id string) (agent.Plan, error) {
	var body string
	err := plans.db.QueryRowContext(ctx, plans.bind(`SELECT body FROM `+table+` WHERE id = ? AND owner = ?`), id, owner).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.Plan{}, agent.ErrNoPlan
	}
	if err != nil {
		return agent.Plan{}, fmt.Errorf("sqlplans: get plan %s: %w", id, err)
	}
	return decode(body)
}

func (plans *store) List(ctx context.Context, owner string, status agent.PlanStatus) ([]agent.Plan, error) {
	query, args := `SELECT body FROM `+table+` WHERE owner = ?`, []any{owner}
	if status != "" {
		query, args = query+` AND status = ?`, append(args, string(status))
	}
	rows, err := plans.db.QueryContext(ctx, plans.bind(query+` ORDER BY created_at DESC`), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlplans: list plans: %w", err)
	}
	defer rows.Close()
	listed := []agent.Plan{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		plan, err := decode(body)
		if err != nil {
			return nil, err
		}
		listed = append(listed, plan)
	}
	return listed, rows.Err()
}

func (plans *store) Update(ctx context.Context, plan *agent.Plan) error {
	read := plan.Version
	plan.Version++
	body, err := json.Marshal(plan)
	if err != nil {
		plan.Version = read
		return err
	}
	result, err := plans.db.ExecContext(ctx, plans.bind(
		`UPDATE `+table+` SET status = ?, version = ?, body = ? WHERE id = ? AND owner = ? AND version = ?`),
		string(plan.Status), plan.Version, string(body), plan.ID, plan.Owner, read)
	if err != nil {
		plan.Version = read
		return fmt.Errorf("sqlplans: update plan %s: %w", plan.ID, err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed == 1 {
		return err
	}
	plan.Version = read
	if _, err := plans.Get(ctx, plan.Owner, plan.ID); err != nil {
		return err
	}
	return agent.ErrPlanChanged
}

func decode(body string) (agent.Plan, error) {
	var plan agent.Plan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return agent.Plan{}, fmt.Errorf("sqlplans: decode plan: %w", err)
	}
	return plan, nil
}
