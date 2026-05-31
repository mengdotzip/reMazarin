package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mdobak/go-xerrors"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Storage struct {
	db *sql.DB
}

func New(path string) (*Storage, error) {
	slog.Info("opening database", "path", path)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, xerrors.Newf("open database: %w", err)
	}

	// WAL mode lets readers proceed concurrently with the single writer.
	// busy_timeout makes writers retry instead of immediately failing with SQLITE_BUSY.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, xerrors.Newf("set wal mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, xerrors.Newf("set busy timeout: %w", err)
	}

	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)

	s := &Storage{db: db}

	if err := s.runMigrations(); err != nil {
		db.Close()
		return nil, xerrors.Newf("run migrations: %w", err)
	}

	s.seedAdmin()
	return s, nil
}

// runMigrations applies any unapplied numbered SQL migrations from the
// embedded migrations/ directory.
func (s *Storage) runMigrations() error {
	// The tracking table is the only thing we create outside of a migration file.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER  PRIMARY KEY,
			name       TEXT     NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return xerrors.Newf("create schema_migrations: %w", err)
	}

	// For databases created before the migration system existed, detect which
	// migrations are already applied by inspecting column presence.
	if err := s.bootstrapMigrations(); err != nil {
		return xerrors.Newf("bootstrap migrations: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return xerrors.Newf("read migrations dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}

		// Filename format: NNN_description.sql
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			slog.Warn("migration skipped: unparseable version", "file", name)
			continue
		}

		var applied bool
		s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, version).Scan(&applied)
		if applied {
			continue
		}

		sql, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return xerrors.Newf("read migration %s: %w", name, err)
		}

		// Run each statement in the file individually so SQLite can handle them.
		for _, stmt := range splitSQL(string(sql)) {
			if _, err := s.db.Exec(stmt); err != nil {
				return xerrors.Newf("apply migration %s: %w", name, err)
			}
		}

		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, version, name,
		); err != nil {
			return xerrors.Newf("record migration %s: %w", name, err)
		}
		slog.Info("applied migration", "version", version, "name", name)
	}

	return nil
}

// bootstrapMigrations marks migrations as applied for databases that were
// created before the migration system existed, using column presence as a
// proxy for which changes have already been made.
func (s *Storage) bootstrapMigrations() error {
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	if count > 0 {
		return nil // Already bootstrapped or on a freshly migrated DB.
	}

	// If proxy_routes doesn't exist this is a brand-new DB — let migrations run.
	var tableExists int
	s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='proxy_routes'`).Scan(&tableExists)
	if tableExists == 0 {
		return nil
	}

	// Old DB: infer applied versions from column presence.
	checks := []struct {
		version int
		name    string
		applied bool
	}{
		{1, "001_initial_schema.sql", true}, // proxy_routes exists → 001 applied
		{2, "002_access_control.sql", s.columnExists("proxy_routes", "allowed_groups")},
		{3, "003_allowed_ips.sql", s.columnExists("proxy_routes", "allowed_ips")},
		{4, "004_ip_session_auth.sql", s.columnExists("proxy_routes", "ip_auth")},
		{5, "005_session_duration.sql", s.columnExists("proxy_routes", "session_duration")},
	}

	for _, c := range checks {
		if !c.applied {
			continue
		}
		if _, err := s.db.Exec(
			`INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (?, ?)`,
			c.version, c.name,
		); err != nil {
			return xerrors.Newf("bootstrap v%d: %w", c.version, err)
		}
	}

	slog.Info("bootstrapped migration tracking from existing schema")
	return nil
}

// columnExists reports whether the given column is present in the given table.
func (s *Storage) columnExists(table, column string) bool {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltVal sql.NullString
		var pk int
		rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk)
		if name == column {
			return true
		}
	}
	return false
}

// splitSQL splits a SQL script into individual statements, correctly handling
// SQLite trigger bodies (BEGIN...END blocks) that contain semicolons.
func splitSQL(s string) []string {
	var stmts []string
	var buf strings.Builder
	depth := 0

	for _, line := range strings.Split(s, "\n") {
		upper := strings.ToUpper(strings.TrimSpace(line))
		if upper == "BEGIN" {
			depth++
		} else if upper == "END" || upper == "END;" {
			depth--
		}
		buf.WriteString(line)
		buf.WriteString("\n")
		if depth == 0 && strings.HasSuffix(strings.TrimSpace(line), ";") {
			if stmt := strings.TrimSpace(buf.String()); stmt != "" && stmt != ";" {
				stmts = append(stmts, stmt)
			}
			buf.Reset()
		}
	}
	if stmt := strings.TrimSpace(buf.String()); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}

// seedAdmin creates the default admin user and group on first run.
func (s *Storage) seedAdmin() {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n > 0 {
		return
	}
	ctx := context.Background()
	user, err := s.CreateUser(ctx, "admin", "admin123")
	if err != nil {
		slog.Error("seed admin user", "error", err)
		return
	}
	group, err := s.CreateGroup(ctx, "admin", "Administrators")
	if err != nil {
		slog.Error("seed admin group", "error", err)
		return
	}
	if err := s.AddUserToGroup(ctx, user.ID, group.ID); err != nil {
		slog.Error("seed admin group membership", "error", err)
		return
	}
	slog.Info("default admin created", "username", "admin", "password", "admin123")
}

func (s *Storage) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Storage) Close() error {
	slog.Info("closing database")
	return s.db.Close()
}

// randHex returns n random bytes as a lowercase hex string.
func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// sha256hex returns the SHA-256 digest of s as a lowercase hex string.
func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
