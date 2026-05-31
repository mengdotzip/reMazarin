package storage

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/mdobak/go-xerrors"
)

type Session struct {
	ID        int
	UserID    int
	Username  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (s *Storage) CreateSession(ctx context.Context, userID int, dur time.Duration, clientIP string) (string, error) {
	tok := randHex(32)
	hash := sha256hex(tok)
	exp := time.Now().Add(dur)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, client_ip, expires_at) VALUES (?, ?, ?, ?)`,
		hash, userID, clientIP, exp)
	if err != nil {
		return "", xerrors.Newf("create session: %w", err)
	}
	slog.Info("session created", "user_id", userID, "client_ip", clientIP)
	return tok, nil
}

// ValidateSession looks up a session by its token
func (s *Storage) ValidateSession(ctx context.Context, tok string) (*Session, error) {
	hash := sha256hex(tok)
	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.user_id, u.username, s.expires_at, s.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`,
		hash, time.Now(),
	).Scan(&sess.ID, &sess.UserID, &sess.Username, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		return nil, xerrors.Newf("invalid or expired session")
	}
	return &sess, nil
}

// ValidateSessionByIP returns any active session whose client_ip matches the
// given address. Used by IP-session auth to authenticate TCP connections and
// cookie-less HTTP requests.
func (s *Storage) ValidateSessionByIP(ctx context.Context, ip string) (*Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.user_id, u.username, s.expires_at, s.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.client_ip = ? AND s.expires_at > ?
		 ORDER BY s.expires_at DESC LIMIT 1`,
		ip, time.Now(),
	).Scan(&sess.ID, &sess.UserID, &sess.Username, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		return nil, xerrors.Newf("no active session for IP")
	}
	return &sess, nil
}

// SessionWithGroups bundles a session with the IDs of all groups the user belongs to.
// Used by the proxy hot path to validate session and group membership in one query.
type SessionWithGroups struct {
	Session
	GroupIDs []int
}

// ValidateSessionAndGroups validates a cookie token and returns the session along
// with all group IDs for the user in a single query, avoiding a second round-trip.
func (s *Storage) ValidateSessionAndGroups(ctx context.Context, tok string) (*SessionWithGroups, error) {
	hash := sha256hex(tok)
	var sg SessionWithGroups
	var groupStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, u.username, s.expires_at, s.created_at,
		       COALESCE(GROUP_CONCAT(ug.group_id), '') AS group_ids
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN user_groups ug ON ug.user_id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?
		GROUP BY s.id, s.user_id, u.username, s.expires_at, s.created_at`,
		hash, time.Now(),
	).Scan(&sg.ID, &sg.UserID, &sg.Username, &sg.ExpiresAt, &sg.CreatedAt, &groupStr)
	if err != nil {
		return nil, xerrors.Newf("invalid or expired session")
	}
	sg.GroupIDs = parseGroupIDs(groupStr)
	return &sg, nil
}

// ValidateSessionByIPAndGroups finds the most recent active session for the given IP
// and returns it with all group IDs in a single query.
func (s *Storage) ValidateSessionByIPAndGroups(ctx context.Context, ip string) (*SessionWithGroups, error) {
	var sg SessionWithGroups
	var groupStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, u.username, s.expires_at, s.created_at,
		       COALESCE(GROUP_CONCAT(ug.group_id), '') AS group_ids
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN user_groups ug ON ug.user_id = s.user_id
		WHERE s.id = (
		    SELECT id FROM sessions
		    WHERE client_ip = ? AND expires_at > ?
		    ORDER BY expires_at DESC LIMIT 1
		)
		GROUP BY s.id, s.user_id, u.username, s.expires_at, s.created_at`,
		ip, time.Now(),
	).Scan(&sg.ID, &sg.UserID, &sg.Username, &sg.ExpiresAt, &sg.CreatedAt, &groupStr)
	if err != nil {
		return nil, xerrors.Newf("no active session for IP")
	}
	sg.GroupIDs = parseGroupIDs(groupStr)
	return &sg, nil
}

func parseGroupIDs(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	ids := make([]int, 0, len(parts))
	for _, p := range parts {
		if id, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Storage) DeleteSession(ctx context.Context, tok string) error {
	hash := sha256hex(tok)
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash)
	return err
}

func (s *Storage) ExtendSession(ctx context.Context, tok string, dur time.Duration) {
	hash := sha256hex(tok)
	exp := time.Now().Add(dur)
	s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, exp, hash)
}

// ExtendSessionByID extends a session by its primary key. Used by IP session
// auth where the token is not available.
func (s *Storage) ExtendSessionByID(ctx context.Context, id int, dur time.Duration) {
	exp := time.Now().Add(dur)
	s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE id = ?`, exp, id)
}

func (s *Storage) CleanupExpiredSessions(ctx context.Context) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now())
	if err != nil {
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		slog.Info("cleaned expired sessions", "count", n)
	}
}
