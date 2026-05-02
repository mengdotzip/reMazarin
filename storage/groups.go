package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/mdobak/go-xerrors"
)

type Group struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Storage) CreateGroup(ctx context.Context, name, description string) (*Group, error) {
	var g Group
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO groups (name, description) VALUES (?, ?) RETURNING id, name, description, created_at`,
		name, description,
	).Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	if err != nil {
		return nil, xerrors.Newf("create group: %w", err)
	}
	slog.Info("group created", "name", name)
	return &g, nil
}

func (s *Storage) GetAllGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, created_at FROM groups ORDER BY name`)
	if err != nil {
		return nil, xerrors.Newf("query groups: %w", err)
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return nil, xerrors.Newf("scan group: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Storage) DeleteGroup(ctx context.Context, id int) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return xerrors.Newf("delete group: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return xerrors.Newf("group not found")
	}
	slog.Info("group deleted", "id", id)
	return nil
}

func (s *Storage) GetUserGroups(ctx context.Context, userID int) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.description, g.created_at
		FROM groups g
		JOIN user_groups ug ON g.id = ug.group_id
		WHERE ug.user_id = ?
		ORDER BY g.name`, userID)
	if err != nil {
		return nil, xerrors.Newf("query user groups: %w", err)
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return nil, xerrors.Newf("scan group: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Storage) AddUserToGroup(ctx context.Context, userID, groupID int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO user_groups (user_id, group_id) VALUES (?, ?)`, userID, groupID)
	if err != nil {
		return xerrors.Newf("add user to group: %w", err)
	}
	return nil
}

func (s *Storage) RemoveUserFromGroup(ctx context.Context, userID, groupID int) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM user_groups WHERE user_id = ? AND group_id = ?`, userID, groupID)
	return err
}

// UserInGroup returns true if the user belongs to the group with the given name.
func (s *Storage) UserInGroup(ctx context.Context, userID int, groupName string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_groups ug
		JOIN groups g ON g.id = ug.group_id
		WHERE ug.user_id = ? AND g.name = ?`, userID, groupName,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
