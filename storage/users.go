package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/mdobak/go-xerrors"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Storage) CreateUser(ctx context.Context, username, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, xerrors.Newf("hash password: %w", err)
	}

	var u User
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash) VALUES (?, ?) RETURNING id, username, created_at`,
		username, string(hash),
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if err != nil {
		return nil, xerrors.Newf("create user: %w", err)
	}
	slog.Info("user created", "username", username)
	return &u, nil
}

// Authenticate looks up a user by username and verifies the password.
// Returns the user on success or an error on failure (user not found or wrong password).
func (s *Storage) Authenticate(ctx context.Context, username, password string) (*User, error) {
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &hash, &u.CreatedAt)
	if err != nil {
		return nil, xerrors.Newf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, xerrors.Newf("invalid credentials")
	}
	return &u, nil
}

func (s *Storage) GetUserByID(ctx context.Context, id int) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if err != nil {
		return nil, xerrors.Newf("user not found: %w", err)
	}
	return &u, nil
}

func (s *Storage) GetAllUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, xerrors.Newf("query users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.CreatedAt); err != nil {
			return nil, xerrors.Newf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Storage) DeleteUser(ctx context.Context, id int) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return xerrors.Newf("delete user: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return xerrors.Newf("user not found")
	}
	slog.Info("user deleted", "id", id)
	return nil
}
