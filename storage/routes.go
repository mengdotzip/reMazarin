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
	PersistentLogin bool      `json:"persistent_login"`
	RangeGroup      string    `json:"range_group"`
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
// upserted (preserving access-control settings) and config routes no longer
// present in the file are deleted.
func (s *Storage) SyncRoutes(routes []ConfigRoute) error {
	slog.Info("syncing routes", "count", len(routes))

	tx, err := s.db.Begin()
	if err != nil {
		return xerrors.Newf("begin tx: %w", err)
	}
	defer tx.Rollback()

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

	// Delete config routes no longer present in the config file.
	if len(routes) == 0 {
		if _, err := tx.Exec(`DELETE FROM proxy_routes WHERE source = 'config'`); err != nil {
			return xerrors.Newf("delete removed config routes: %w", err)
		}
	} else {
		placeholders := strings.Repeat("?,", len(routes))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(routes))
		for i, r := range routes {
			args[i] = r.Url
		}
		if _, err := tx.Exec(`DELETE FROM proxy_routes WHERE source = 'config' AND url NOT IN (`+placeholders+`)`, args...); err != nil {
			return xerrors.Newf("delete removed config routes: %w", err)
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
		       allowed_groups, allowed_ips, ip_auth, persistent_login, range_group, created_at, updated_at
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
			&r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.PersistentLogin, &r.RangeGroup,
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
		       allowed_groups, allowed_ips, ip_auth, persistent_login, range_group, created_at, updated_at
		FROM proxy_routes WHERE url = ? AND enabled = TRUE`, url,
	).Scan(
		&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
		&r.Enabled, &r.Source,
		&r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.PersistentLogin, &r.RangeGroup,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, xerrors.Newf("get route: %w", err)
	}
	return &r, nil
}

// UpdateRouteAccess updates access-control fields for a route. It does NOT touch
// routing config (url, target, type, tls, cert, key).
func (s *Storage) UpdateRouteAccess(ctx context.Context, id int, allowedGroups, allowedIPs string, ipAuth, persistentLogin bool) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE proxy_routes SET allowed_groups = ?, allowed_ips = ?, ip_auth = ?, persistent_login = ? WHERE id = ?`,
		allowedGroups, allowedIPs, ipAuth, persistentLogin, id)
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

// CreateRoute adds a new route created via the admin UI (source = 'ui'). For a
// single route rangeGroup is empty; for one port of an expanded port range it
// carries the shared range id linking all ports of that range together.
func (s *Storage) CreateRoute(ctx context.Context, url, target, routeType string, tls bool, cert, key, rangeGroup string) (*Route, error) {
	var r Route
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO proxy_routes (url, target, type, tls, cert, key, source, enabled, range_group)
		VALUES (?, ?, ?, ?, ?, ?, 'ui', TRUE, ?)
		RETURNING id, url, target, type, tls, cert, key, enabled, source,
		          allowed_groups, allowed_ips, ip_auth, persistent_login, range_group, created_at, updated_at`,
		url, target, routeType, tls, cert, key, rangeGroup,
	).Scan(&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
		&r.Enabled, &r.Source, &r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.PersistentLogin, &r.RangeGroup,
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

// UpdateRouteAccessByGroup applies the same access-control settings to every
// route in a port-range group. Access fields are identical across all ports of
// a range, so a single UPDATE covers them. Returns the number of rows updated.
func (s *Storage) UpdateRouteAccessByGroup(ctx context.Context, rangeGroup, allowedGroups, allowedIPs string, ipAuth, persistentLogin bool) (int, error) {
	if rangeGroup == "" {
		return 0, xerrors.Newf("empty range group")
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE proxy_routes SET allowed_groups = ?, allowed_ips = ?, ip_auth = ?, persistent_login = ? WHERE range_group = ?`,
		allowedGroups, allowedIPs, ipAuth, persistentLogin, rangeGroup)
	if err != nil {
		return 0, xerrors.Newf("update route access by group: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return 0, xerrors.Newf("range group not found")
	}
	slog.Info("route range access updated", "range_group", rangeGroup, "count", n, "allowed_groups", allowedGroups, "allowed_ips", allowedIPs, "ip_auth", ipAuth)
	return int(n), nil
}

// DeleteRouteGroup deletes every UI-sourced route in a port-range group and
// returns their URLs for proxy cleanup.
func (s *Storage) DeleteRouteGroup(ctx context.Context, rangeGroup string) ([]string, error) {
	if rangeGroup == "" {
		return nil, xerrors.Newf("empty range group")
	}
	rows, err := s.db.QueryContext(ctx,
		`DELETE FROM proxy_routes WHERE range_group = ? AND source = 'ui' RETURNING url`, rangeGroup)
	if err != nil {
		return nil, xerrors.Newf("delete route group: %w", err)
	}
	defer rows.Close()
	var urls []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, xerrors.Newf("scan deleted url: %w", err)
		}
		urls = append(urls, url)
	}
	if len(urls) == 0 {
		return nil, xerrors.Newf("range group not found")
	}
	slog.Info("route range deleted", "range_group", rangeGroup, "count", len(urls))
	return urls, rows.Err()
}

// GetRouteByGroup returns one representative route from a port-range group, used
// to inspect shared properties (type, source) without loading the whole range.
func (s *Storage) GetRouteByGroup(ctx context.Context, rangeGroup string) (*Route, error) {
	var r Route
	err := s.db.QueryRowContext(ctx, `
		SELECT id, url, target, type, tls, cert, key, enabled, source,
		       allowed_groups, allowed_ips, ip_auth, persistent_login, range_group, created_at, updated_at
		FROM proxy_routes WHERE range_group = ? ORDER BY url LIMIT 1`, rangeGroup,
	).Scan(&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
		&r.Enabled, &r.Source, &r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.PersistentLogin, &r.RangeGroup,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, xerrors.Newf("get route by group: %w", err)
	}
	return &r, nil
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
		       allowed_groups, allowed_ips, ip_auth, persistent_login, range_group, created_at, updated_at
		FROM proxy_routes WHERE id = ?`, id,
	).Scan(&r.ID, &r.Url, &r.Target, &r.Type, &r.Tls, &r.Cert, &r.Key,
		&r.Enabled, &r.Source, &r.AllowedGroups, &r.AllowedIPs, &r.IPAuth, &r.PersistentLogin, &r.RangeGroup,
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

// NewRangeGroupID returns a fresh random identifier shared by all ports of a
// single port-range route.
func NewRangeGroupID() string { return randHex(8) }

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
