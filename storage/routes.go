package storage

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/mdobak/go-xerrors"
)

type Route struct {
	ID              int       `json:"id"`
	Url             string    `json:"url"`
	Target          string    `json:"target"`
	Type            string    `json:"type"`
	Tls             bool      `json:"tls"`
	Cert            string    `json:"-"`
	Key             string    `json:"-"`
	Enabled         bool      `json:"enabled"`
	Source          string    `json:"source"`
	AllowedGroups   string    `json:"allowed_groups"`
	AllowedIPs      string    `json:"allowed_ips"`
	IPAuth          bool      `json:"ip_auth"`
	CookiePolicy    string    `json:"cookie_policy"`
	RenewOnAccess   bool      `json:"renew_on_access"`
	SessionDuration int       `json:"session_duration"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ConfigRoute struct {
	Url    string
	Target string
	Type   string
	Tls    bool
	Cert   string
	Key    string
}

// SyncRoutes reconciles DB routes with the config file. Config routes are
// upserted, preserving any access-control settings already stored in the DB.
// Routes removed from config are disabled (not deleted) so their settings survive restarts.
func (s *Storage) SyncRoutes(routes []ConfigRoute) error {
	slog.Info("syncing routes", "count", len(routes))

	tx, err := s.db.Begin()
	if err != nil {
		return xerrors.Newf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Disable all config-sourced routes first; the upsert re-enables those still present.
	if _, err := tx.Exec(`UPDATE proxy_routes SET enabled = FALSE WHERE source = 'config'`); err != nil {
		return xerrors.Newf("disable config routes: %w", err)
	}

	for _, r := range routes {
		_, err := tx.Exec(`
			INSERT INTO proxy_routes (url, target, type, tls, cert, key, source, enabled)
			VALUES (?, ?, ?, ?, ?, ?, 'config', TRUE)
			ON CONFLICT(url) DO UPDATE SET
				target  = excluded.target,
				type    = excluded.type,
				tls     = excluded.tls,
				cert    = excluded.cert,
				key     = excluded.key,
				source  = excluded.source,
				enabled = TRUE
		`, r.Url, r.Target, r.Type, r.Tls, r.Cert, r.Key)
		if err != nil {
			return xerrors.Newf("upsert route %s: %w", r.Url, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Newf("commit: %w", err)
	}

	slog.Info("routes synced")
	return nil
}

func (s *Storage) GetAllRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, url, target, type, tls, cert, key, enabled, source,
		       allowed_groups, allowed_ips, ip_auth, cookie_policy, renew_on_access, session_duration, created_at, updated_at
		FROM proxy_routes
		WHERE enabled = TRUE
		ORDER BY url`)
	if err != nil {
		return nil, xerrors.Newf("query routes: %w", err)
	}
	defer rows.Close()

	var routes []Route
	for rows.Next() {
		var r Route
		if err := rows.Scan(
			&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
			&r.Enabled, &r.Source,
			&r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.CookiePolicy, &r.RenewOnAccess, &r.SessionDuration,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, xerrors.Newf("scan route: %w", err)
		}
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

func (s *Storage) GetRouteByUrl(ctx context.Context, url string) (*Route, error) {
	var r Route
	err := s.db.QueryRowContext(ctx, `
		SELECT id, url, target, type, tls, cert, key, enabled, source,
		       allowed_groups, allowed_ips, ip_auth, cookie_policy, renew_on_access, session_duration, created_at, updated_at
		FROM proxy_routes WHERE url = ? AND enabled = TRUE`, url,
	).Scan(
		&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
		&r.Enabled, &r.Source,
		&r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.CookiePolicy, &r.RenewOnAccess, &r.SessionDuration,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, xerrors.Newf("get route: %w", err)
	}
	return &r, nil
}

// UpdateRouteAccess updates access-control fields for a route. It does NOT touch
// routing config (url, target, type, tls, cert, key).
func (s *Storage) UpdateRouteAccess(ctx context.Context, id int, allowedGroups, allowedIPs string, ipAuth bool, cookiePolicy string, renewOnAccess bool, sessionDuration int) error {
	if sessionDuration <= 0 {
		sessionDuration = 168
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE proxy_routes SET allowed_groups = ?, allowed_ips = ?, ip_auth = ?, cookie_policy = ?, renew_on_access = ?, session_duration = ? WHERE id = ?`,
		allowedGroups, allowedIPs, ipAuth, cookiePolicy, renewOnAccess, sessionDuration, id)
	if err != nil {
		return xerrors.Newf("update route access: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return xerrors.Newf("route not found")
	}
	slog.Info("route access updated", "id", id, "allowed_groups", allowedGroups, "allowed_ips", allowedIPs, "ip_auth", ipAuth)
	return nil
}

// CreateRoute adds a new route created via the admin UI (source = 'ui').
func (s *Storage) CreateRoute(ctx context.Context, url, target, routeType string) (*Route, error) {
	var r Route
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO proxy_routes (url, target, type, source, enabled, cookie_policy)
		VALUES (?, ?, ?, 'ui', TRUE, 'persistent')
		RETURNING id, url, target, type, tls, cert, key, enabled, source,
		          allowed_groups, allowed_ips, ip_auth, cookie_policy, renew_on_access, session_duration, created_at, updated_at`,
		url, target, routeType,
	).Scan(&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
		&r.Enabled, &r.Source, &r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.CookiePolicy, &r.RenewOnAccess, &r.SessionDuration,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, xerrors.Newf("create route: %w", err)
	}
	slog.Info("route created", "url", url)
	return &r, nil
}

// DeleteRoute deletes a UI-sourced route and returns its URL for proxy cleanup.
// Returns an error if the route is config-sourced (those are immutable).
func (s *Storage) DeleteRoute(ctx context.Context, id int) (string, error) {
	var url string
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM proxy_routes WHERE id = ? AND source = 'ui' RETURNING url`, id,
	).Scan(&url)
	if err != nil {
		return "", xerrors.Newf("delete route: %w", err)
	}
	slog.Info("route deleted", "id", id)
	return url, nil
}

// UpdateRouteEndpoint updates the backend target for a UI-sourced route.
func (s *Storage) UpdateRouteEndpoint(ctx context.Context, id int, target string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE proxy_routes SET target = ? WHERE id = ? AND source = 'ui'`, target, id)
	if err != nil {
		return xerrors.Newf("update route endpoint: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return xerrors.Newf("route not found or not editable")
	}
	slog.Info("route endpoint updated", "id", id)
	return nil
}

// GetRouteByID fetches a single route by its primary key.
func (s *Storage) GetRouteByID(ctx context.Context, id int) (*Route, error) {
	var r Route
	err := s.db.QueryRowContext(ctx, `
		SELECT id, url, target, type, tls, cert, key, enabled, source,
		       allowed_groups, allowed_ips, ip_auth, cookie_policy, renew_on_access, session_duration, created_at, updated_at
		FROM proxy_routes WHERE id = ?`, id,
	).Scan(&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
		&r.Enabled, &r.Source, &r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.CookiePolicy, &r.RenewOnAccess, &r.SessionDuration,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, xerrors.Newf("get route by id: %w", err)
	}
	return &r, nil
}

// EnsureRouteGroup sets the allowed_groups for the given route URL to the
// named group's ID, but only if allowed_groups is currently empty. This is used
// at startup to protect system routes (e.g. the admin panel) without overriding
// any admin-configured access control.
func (s *Storage) EnsureRouteGroup(ctx context.Context, routeURL, groupName string) error {
	var groupID int
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM groups WHERE name = ?`, groupName).Scan(&groupID); err != nil {
		return xerrors.Newf("group %q not found: %w", groupName, err)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE proxy_routes SET allowed_groups = ?
		WHERE url = ? AND (allowed_groups = '' OR allowed_groups IS NULL)`,
		strconv.Itoa(groupID), routeURL)
	if err != nil {
		return xerrors.Newf("ensure route group: %w", err)
	}
	return nil
}

// RouteAllows returns true if the given group IDs satisfy the route's allowed_groups.
// An empty allowed_groups string means the route is public.
func RouteAllows(allowedGroups string, groupIDs []int) bool {
	if allowedGroups == "" {
		return true
	}
	allowed := make(map[string]bool)
	for _, part := range strings.Split(allowedGroups, ",") {
		p := strings.TrimSpace(part)
		if p != "" {
			allowed[p] = true
		}
	}
	for _, id := range groupIDs {
		if allowed[strconv.Itoa(id)] {
			return true
		}
	}
	return false
}
