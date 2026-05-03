package storage

import (
	"context"
	"log/slog"
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

// CreateSession generates a secure random token, stores its hash alongside the
// client IP, and returns the plaintext token (to be given to the client as a cookie).
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

// ValidateSession looks up a session by its token. Returns the session or an error
// if the token is invalid or expired.
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
