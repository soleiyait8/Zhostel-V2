// Package audit writes append-only audit records.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Execer is satisfied by *sql.DB and *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Entry describes one change.
type Entry struct {
	Actor      string // user id, "" for system
	Action     string
	EntityType string
	EntityID   string
	Before     any
	After      any
	SourceIP   string
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func asJSON(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// Write records e. Call it inside the same transaction as the change.
func Write(ctx context.Context, x Execer, e Entry) error {
	_, err := x.ExecContext(ctx, `INSERT INTO audit_logs
		(actor_user_id, action, entity_type, entity_id, before_data, after_data, source_ip)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7::inet)`,
		nullable(e.Actor), e.Action, e.EntityType, nullable(e.EntityID),
		asJSON(e.Before), asJSON(e.After), nullable(e.SourceIP))
	return err
}
