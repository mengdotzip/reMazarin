package storage

import (
	"context"
	"time"

	"github.com/mdobak/go-xerrors"
)

type AuthFailure struct {
	ID        int       `json:"id"`
	IP        string    `json:"ip"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

type SessionInfo struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	Username  string    `json:"username"`
	ClientIP  string    `json:"client_ip"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Storage) LogAuthFailure(ctx context.Context, ip, username string) {
	s.db.ExecContext(ctx, `INSERT INTO auth_failures (ip, username) VALUES (?, ?)`, ip, username)
}

func (s *Storage) GetRecentAuthFailures(ctx context.Context, limit int) ([]AuthFailure, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ip, username, created_at FROM auth_failures ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, xerrors.Newf("query auth failures: %w", err)
	}
	defer rows.Close()
	var out []AuthFailure
	for rows.Next() {
		var f AuthFailure
		rows.Scan(&f.ID, &f.IP, &f.Username, &f.CreatedAt)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Storage) GetActiveSessions(ctx context.Context) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.user_id, u.username, s.client_ip, s.created_at, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.expires_at > datetime('now')
		ORDER BY s.created_at DESC`)
	if err != nil {
		return nil, xerrors.Newf("query sessions: %w", err)
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		rows.Scan(&si.ID, &si.UserID, &si.Username, &si.ClientIP, &si.CreatedAt, &si.ExpiresAt)
		out = append(out, si)
	}
	return out, rows.Err()
}

func (s *Storage) GetUserSessions(ctx context.Context, userID int) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.user_id, u.username, s.client_ip, s.created_at, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.user_id = ? AND s.expires_at > datetime('now')
		ORDER BY s.created_at DESC`, userID)
	if err != nil {
		return nil, xerrors.Newf("query user sessions: %w", err)
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		rows.Scan(&si.ID, &si.UserID, &si.Username, &si.ClientIP, &si.CreatedAt, &si.ExpiresAt)
		out = append(out, si)
	}
	return out, rows.Err()
}

// DeleteSessionByID deletes a session owned by ownerUserID. Used for user self-revocation.
func (s *Storage) DeleteSessionByID(ctx context.Context, sessionID, ownerUserID int) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, ownerUserID)
	if err != nil {
		return xerrors.Newf("delete session: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return xerrors.Newf("session not found")
	}
	return nil
}

// AdminDeleteSession force-deletes any session by ID. Used by admins.
func (s *Storage) AdminDeleteSession(ctx context.Context, sessionID int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// ── access log ────────────────────────────────────────────────────────────────

type AccessEvent struct {
	ID        int       `json:"id"`
	IP        string    `json:"ip"`
	Username  string    `json:"username"`
	RouteUrl  string    `json:"route_url"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Storage) LogAccess(ctx context.Context, ip, username, routeUrl string) {
	s.db.ExecContext(ctx,
		`INSERT INTO access_log (ip, username, route_url) VALUES (?, ?, ?)`,
		ip, username, routeUrl)
}

func (s *Storage) GetRecentAccess(ctx context.Context, limit int) ([]AccessEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ip, username, route_url, created_at FROM access_log ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, xerrors.Newf("query access_log: %w", err)
	}
	defer rows.Close()
	var out []AccessEvent
	for rows.Next() {
		var e AccessEvent
		rows.Scan(&e.ID, &e.IP, &e.Username, &e.RouteUrl, &e.CreatedAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Storage) CleanupOldAccessLog(ctx context.Context) {
	s.db.ExecContext(ctx,
		`DELETE FROM access_log WHERE id NOT IN (SELECT id FROM access_log ORDER BY created_at DESC LIMIT 5000)`)
}
