package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/mdobak/go-xerrors"
)

type Route struct {
	ID        int
	Url       string
	Target    string
	Type      string
	Tls       bool
	Cert      string
	Key       string
	Enabled   bool
	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ConfigRoute struct {
	Url    string
	Target string
	Type   string
	Tls    bool
	Cert   string
	Key    string
}

func (s *Storage) SyncRoutes(routes []ConfigRoute) error {
	slog.Info("syncing routes from config", "count", len(routes))

	tx, err := s.db.Begin()
	if err != nil {
		return xerrors.Newf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete all old config routes
	deleteQuery := `DELETE FROM proxy_routes WHERE source = 'config'`
	result, err := tx.Exec(deleteQuery)
	if err != nil {
		return xerrors.Newf("delete old config routes: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return xerrors.Newf("getting affected rows: %w", err)
	}
	if deleted > 0 {
		slog.Info("deleted old config routes", "count", deleted)
	}

	// Insert fresh routes from TOML
	insertQuery := `
		INSERT INTO proxy_routes (url, target, type, tls, cert, key, source)
		VALUES (?, ?, ?, ?, ?, ?, 'config')
	`

	for _, route := range routes {
		_, err := tx.Exec(insertQuery, route.Url, route.Target, route.Type, route.Tls, route.Cert, route.Key)
		if err != nil {
			return xerrors.Newf("insert route %s: %w", route.Url, err)
		}
		slog.Debug("route inserted", "url", route.Url, "target", route.Target)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Newf("commit transaction: %w", err)
	}

	slog.Info("routes synced successfully", "inserted", len(routes))
	return nil
}

func (s *Storage) GetAllRoutes(ctx context.Context) ([]Route, error) {
	query := `
		SELECT id, url, target, type, tls, cert, key, enabled, source, created_at, updated_at
		FROM proxy_routes
		WHERE enabled = TRUE
		ORDER BY url
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, xerrors.Newf("query routes: %w", err)
	}
	defer rows.Close()

	var routes []Route
	for rows.Next() {
		var r Route
		err := rows.Scan(&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key, &r.Enabled, &r.Source, &r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, xerrors.Newf("scan route: %w", err)
		}
		routes = append(routes, r)
	}

	if err := rows.Err(); err != nil {
		return nil, xerrors.Newf("rows error: %w", err)
	}

	return routes, nil
}

func (s *Storage) GetRouteByUrl(ctx context.Context, url string) (*Route, error) {
	query := `
		SELECT id, url, target, type, tls, cert, key, enabled, source, created_at, updated_at
		FROM proxy_routes
		WHERE url = ? AND enabled = TRUE
	`

	var r Route
	err := s.db.QueryRowContext(ctx, query, url).Scan(
		&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls,
		&r.Enabled, &r.Source, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, xerrors.Newf("get route: %w", err)
	}

	return &r, nil
}
