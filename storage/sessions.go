package storage

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mdobak/go-xerrors"
)

// normalizeIP canonicalises an IP string so the same client always maps to the
// same value regardless of how the listener reported it. An IPv4 client can
// appear as "1.2.3.4" on one socket and as the IPv4-mapped form
// "::ffff:1.2.3.4" on another (dual-stack), which are different strings for the
// same address. Session IP auth compares client_ip as a string in SQL, so both
// the stored value and the looked-up value must be normalised identically or
// they will never match. Returns the input unchanged if it is not a valid IP.
func normalizeIP(ip string) string {
	if p := net.ParseIP(strings.TrimSpace(ip)); p != nil {
		return p.String()
	}
	return ip
}

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
	clientIP = normalizeIP(clientIP)

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
	ip = normalizeIP(ip)
	var sess Session
	err := s.db.QueryRowContext(ctx,
		// CAST guards against rows whose client_ip was persisted as BLOB by older
		// builds; a BLOB never compares equal to a bound TEXT value in SQLite.
		`SELECT s.id, s.user_id, u.username, s.expires_at, s.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE CAST(s.client_ip AS TEXT) = ? AND s.expires_at > ?
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

// ValidateSessionByIPAndGroups finds the most recent active session for the given
// IP whose user still exists, returning it with all of that user's group IDs.
func (s *Storage) ValidateSessionByIPAndGroups(ctx context.Context, ip string) (*SessionWithGroups, error) {
	return s.ValidateSessionByIPInGroups(ctx, ip, nil)
}

// ValidateSessionByIPInGroups returns the most recent active session for the
// given IP whose user still exists and — when allowedGroups is non-empty —
// belongs to at least one of those groups. A returned session is already
// authorized for the route, so callers need no further group check.
//
// The inner JOIN to users skips orphaned sessions (rows left behind when a user
// was deleted while foreign keys were not enforced); without it, an orphaned
// session with a later expiry could shadow a co-located valid session and make
// the whole lookup fail. The optional group filter likewise avoids selecting a
// session belonging to a different user on the same IP who lacks access.
func (s *Storage) ValidateSessionByIPInGroups(ctx context.Context, ip string, allowedGroups []int) (*SessionWithGroups, error) {
	ip = normalizeIP(ip)

	// CAST guards against legacy rows whose client_ip was persisted as BLOB.
	args := []any{ip, time.Now()}
	groupFilter := ""
	if len(allowedGroups) > 0 {
		ph := make([]string, len(allowedGroups))
		for i, id := range allowedGroups {
			ph[i] = "?"
			args = append(args, id)
		}
		groupFilter = `AND s2.user_id IN (SELECT user_id FROM user_groups WHERE group_id IN (` +
			strings.Join(ph, ",") + `))`
	}

	query := `
		SELECT s.id, s.user_id, u.username, s.expires_at, s.created_at,
		       COALESCE(GROUP_CONCAT(ug.group_id), '') AS group_ids
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN user_groups ug ON ug.user_id = s.user_id
		WHERE s.id = (
		    SELECT s2.id FROM sessions s2
		    JOIN users u2 ON u2.id = s2.user_id
		    WHERE CAST(s2.client_ip AS TEXT) = ? AND s2.expires_at > ? ` + groupFilter + `
		    ORDER BY s2.expires_at DESC LIMIT 1
		)
		GROUP BY s.id, s.user_id, u.username, s.expires_at, s.created_at`

	var sg SessionWithGroups
	var groupStr string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&sg.ID, &sg.UserID, &sg.Username, &sg.ExpiresAt, &sg.CreatedAt, &groupStr)
	if err != nil {
		return nil, xerrors.Newf("no active session for IP")
	}
	sg.GroupIDs = parseGroupIDs(groupStr)
	return &sg, nil
}

// DebugDumpSessions returns a human-readable snapshot of the most recent
// sessions for diagnostics: the client_ip is quoted (%q) so hidden characters
// or IPv4-mapped forms are visible, and active reflects expires_at > now.
// Intended only for verbose auth debugging.
func (s *Storage) DebugDumpSessions(ctx context.Context, limit int) []string {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, client_ip, typeof(client_ip), typeof(expires_at),
		        expires_at, expires_at > ? AS active
		 FROM sessions ORDER BY id DESC LIMIT ?`,
		time.Now(), limit)
	if err != nil {
		return []string{"dump error: " + err.Error()}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, uid, active int
		var ip, ipType, expType, exp string
		if err := rows.Scan(&id, &uid, &ip, &ipType, &expType, &exp, &active); err != nil {
			out = append(out, "scan error: "+err.Error())
			continue
		}
		out = append(out, fmt.Sprintf("id=%d user=%d ip=%q ip_type=%s exp_type=%s active=%d exp=%s",
			id, uid, ip, ipType, expType, active, exp))
	}
	return out
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
