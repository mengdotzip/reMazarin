package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/mdobak/go-xerrors"
)

type Invite struct {
	ID          int       `json:"id"`
	Description string    `json:"description"`
	Used        bool      `json:"used"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateInvite generates a one-time invite code valid for dur. It returns the
// plaintext code (shown once) and the stored invite record.
func (s *Storage) CreateInvite(ctx context.Context, description string, dur time.Duration) (string, *Invite, error) {
	code := randHex(16)
	hash := sha256hex(code)
	exp := time.Now().Add(dur)

	var inv Invite
	inv.ExpiresAt = exp
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO invites (code_hash, description, expires_at) VALUES (?, ?, ?) RETURNING id, description, used, created_at`,
		hash, description, exp,
	).Scan(&inv.ID, &inv.Description, &inv.Used, &inv.CreatedAt)
	if err != nil {
		return "", nil, xerrors.Newf("create invite: %w", err)
	}
	slog.Info("invite created", "id", inv.ID, "expires_at", exp)
	return code, &inv, nil
}

// UseInvite validates the plaintext code, marks it used, and returns the invite.
// Returns an error if the code is invalid, already used, or expired.
func (s *Storage) UseInvite(ctx context.Context, code string) (*Invite, error) {
	hash := sha256hex(code)
	var inv Invite
	err := s.db.QueryRowContext(ctx,
		`SELECT id, used, expires_at, created_at FROM invites
		 WHERE code_hash = ? AND used = FALSE AND expires_at > ?`,
		hash, time.Now(),
	).Scan(&inv.ID, &inv.Used, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, xerrors.Newf("invalid or expired invite")
	}
	_, err = s.db.ExecContext(ctx, `UPDATE invites SET used = TRUE WHERE id = ?`, inv.ID)
	if err != nil {
		return nil, xerrors.Newf("mark invite used: %w", err)
	}
	inv.Used = true
	return &inv, nil
}

func (s *Storage) GetAllInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, description, used, expires_at, created_at FROM invites ORDER BY created_at DESC`)
	if err != nil {
		return nil, xerrors.Newf("query invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.ID, &inv.Description, &inv.Used, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, xerrors.Newf("scan invite: %w", err)
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

func (s *Storage) DeleteInvite(ctx context.Context, id int) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE id = ?`, id)
	if err != nil {
		return xerrors.Newf("delete invite: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return xerrors.Newf("invite not found")
	}
	return nil
}

func (s *Storage) CleanupExpiredInvites(ctx context.Context) {
	s.db.ExecContext(ctx, `DELETE FROM invites WHERE expires_at < ? AND used = FALSE`, time.Now())
}
