// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite" // pure-Go sqlite driver
)

// SQLiteStore persists approval state in a single SQLite file. WAL for
// concurrent readers; synchronous=FULL because grant single-use must
// survive power loss, and approval traffic is human-scale.
type SQLiteStore struct {
	db  *sql.DB
	log *slog.Logger
}

const approvalSchema = `
CREATE TABLE IF NOT EXISTS approval_requests (
  id                  TEXT PRIMARY KEY,
  subject_key         TEXT NOT NULL,
  subject_json        TEXT NOT NULL,
  sql_hash            TEXT NOT NULL,
  sql_sample          TEXT NOT NULL,
  reasons_json        TEXT NOT NULL,
  policies_json       TEXT NOT NULL,
  accept_token_sha256 TEXT NOT NULL,
  reject_token_sha256 TEXT NOT NULL,
  state               TEXT NOT NULL,
  created_at          TEXT NOT NULL,
  expires_at          TEXT NOT NULL,
  decided_at          TEXT NOT NULL DEFAULT '',
  approver_ip         TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS approval_grants (
  grant_key   TEXT PRIMARY KEY,
  approval_id TEXT NOT NULL,
  expires_at  TEXT NOT NULL
);`

// NewSQLiteStore opens (or creates) the database at path.
func NewSQLiteStore(path string, logger *slog.Logger) (*SQLiteStore, error) {
	if logger == nil {
		logger = slog.Default()
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, fmt.Errorf("approval: open store: %w", err)
	}
	if _, err := db.Exec(approvalSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("approval: migrate store: %w", err)
	}
	return &SQLiteStore{db: db, log: logger}, nil
}

// Load implements Store.
func (s *SQLiteStore) Load(ctx context.Context) ([]StoredRequest, []StoredGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, subject_key, subject_json, sql_hash,
		sql_sample, reasons_json, policies_json, accept_token_sha256, reject_token_sha256,
		state, created_at, expires_at, decided_at, approver_ip FROM approval_requests`)
	if err != nil {
		return nil, nil, fmt.Errorf("approval: load requests: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var reqs []StoredRequest
	for rows.Next() {
		var (
			sr                                     StoredRequest
			subjectJSON, reasonsJSON, policiesJSON string
			created, expires, decided              string
		)
		if err := rows.Scan(&sr.ID, &sr.SubjectKey, &subjectJSON, &sr.SQLHash,
			&sr.SQLSample, &reasonsJSON, &policiesJSON, &sr.AcceptTokenSHA256,
			&sr.RejectTokenSHA256, &sr.State, &created, &expires, &decided,
			&sr.ApproverIP); err != nil {
			return nil, nil, fmt.Errorf("approval: scan request: %w", err)
		}
		if err := json.Unmarshal([]byte(subjectJSON), &sr.Subject); err != nil {
			return nil, nil, fmt.Errorf("approval: request %s subject: %w", sr.ID, err)
		}
		if err := json.Unmarshal([]byte(reasonsJSON), &sr.Reasons); err != nil {
			return nil, nil, fmt.Errorf("approval: request %s reasons: %w", sr.ID, err)
		}
		if err := json.Unmarshal([]byte(policiesJSON), &sr.Policies); err != nil {
			return nil, nil, fmt.Errorf("approval: request %s policies: %w", sr.ID, err)
		}
		if sr.CreatedAt, err = parseStoredTime(created); err != nil {
			return nil, nil, fmt.Errorf("approval: request %s created_at: %w", sr.ID, err)
		}
		if sr.ExpiresAt, err = parseStoredTime(expires); err != nil {
			return nil, nil, fmt.Errorf("approval: request %s expires_at: %w", sr.ID, err)
		}
		if sr.DecidedAt, err = parseStoredTime(decided); err != nil {
			return nil, nil, fmt.Errorf("approval: request %s decided_at: %w", sr.ID, err)
		}
		reqs = append(reqs, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("approval: load requests: %w", err)
	}

	grows, err := s.db.QueryContext(ctx, `SELECT grant_key, approval_id, expires_at FROM approval_grants`)
	if err != nil {
		return nil, nil, fmt.Errorf("approval: load grants: %w", err)
	}
	defer func() { _ = grows.Close() }()

	var grants []StoredGrant
	for grows.Next() {
		var (
			sg      StoredGrant
			expires string
		)
		if err := grows.Scan(&sg.Key, &sg.ApprovalID, &expires); err != nil {
			return nil, nil, fmt.Errorf("approval: scan grant: %w", err)
		}
		if sg.ExpiresAt, err = parseStoredTime(expires); err != nil {
			return nil, nil, fmt.Errorf("approval: grant expires_at: %w", err)
		}
		grants = append(grants, sg)
	}
	if err := grows.Err(); err != nil {
		return nil, nil, fmt.Errorf("approval: load grants: %w", err)
	}
	return reqs, grants, nil
}

// PutRequest implements Store (upsert).
func (s *SQLiteStore) PutRequest(ctx context.Context, r StoredRequest) error {
	subjectJSON, err := json.Marshal(r.Subject)
	if err != nil {
		return fmt.Errorf("approval: marshal subject: %w", err)
	}
	reasonsJSON, err := json.Marshal(r.Reasons)
	if err != nil {
		return fmt.Errorf("approval: marshal reasons: %w", err)
	}
	policiesJSON, err := json.Marshal(r.Policies)
	if err != nil {
		return fmt.Errorf("approval: marshal policies: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO approval_requests
		(id, subject_key, subject_json, sql_hash, sql_sample, reasons_json,
		 policies_json, accept_token_sha256, reject_token_sha256, state,
		 created_at, expires_at, decided_at, approver_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		 state = excluded.state,
		 decided_at = excluded.decided_at,
		 approver_ip = excluded.approver_ip`,
		r.ID, r.SubjectKey, string(subjectJSON), r.SQLHash, r.SQLSample,
		string(reasonsJSON), string(policiesJSON), r.AcceptTokenSHA256,
		r.RejectTokenSHA256, string(r.State), formatStoredTime(r.CreatedAt),
		formatStoredTime(r.ExpiresAt), formatStoredTime(r.DecidedAt), r.ApproverIP)
	if err != nil {
		return fmt.Errorf("approval: put request: %w", err)
	}
	return nil
}

// DeleteRequest implements Store.
func (s *SQLiteStore) DeleteRequest(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM approval_requests WHERE id = ?`, id); err != nil {
		return fmt.Errorf("approval: delete request: %w", err)
	}
	return nil
}

// PutGrant implements Store.
func (s *SQLiteStore) PutGrant(ctx context.Context, g StoredGrant) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO approval_grants (grant_key, approval_id, expires_at)
		VALUES (?, ?, ?)
		ON CONFLICT(grant_key) DO UPDATE SET
		 approval_id = excluded.approval_id,
		 expires_at = excluded.expires_at`,
		g.Key, g.ApprovalID, formatStoredTime(g.ExpiresAt))
	if err != nil {
		return fmt.Errorf("approval: put grant: %w", err)
	}
	return nil
}

// DeleteGrant implements Store.
func (s *SQLiteStore) DeleteGrant(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM approval_grants WHERE grant_key = ?`, key); err != nil {
		return fmt.Errorf("approval: delete grant: %w", err)
	}
	return nil
}

// Close implements Store.
func (s *SQLiteStore) Close(context.Context) error {
	return s.db.Close()
}

func formatStoredTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseStoredTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}
