package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log/slog"

	"github.com/mdobak/go-xerrors"
	_ "modernc.org/sqlite"
)

type Storage struct {
	db *sql.DB
}

func New(path string) (*Storage, error) {
	slog.Info("opening database", "path", path)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, xerrors.Newf("open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Storage{db: db}

	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, xerrors.Newf("init schema: %w", err)
	}

	s.migrate()
	s.seedAdmin()

	return s, nil
}

func (s *Storage) initSchema() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS proxy_routes (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		url           TEXT NOT NULL UNIQUE,
		target        TEXT NOT NULL,
		type          TEXT DEFAULT 'proxy',
		tls           BOOLEAN DEFAULT FALSE,
		cert          TEXT DEFAULT '',
		key           TEXT DEFAULT '',
		enabled       BOOLEAN DEFAULT TRUE,
		source        TEXT NOT NULL,
		allowed_groups TEXT DEFAULT '',
		cookie_policy TEXT DEFAULT 'persistent',
		renew_on_access BOOLEAN DEFAULT FALSE,
		created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TRIGGER IF NOT EXISTS proxy_routes_updated
	AFTER UPDATE ON proxy_routes FOR EACH ROW
	BEGIN
		UPDATE proxy_routes SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
	END;

	CREATE INDEX IF NOT EXISTS idx_routes_url     ON proxy_routes(url);
	CREATE INDEX IF NOT EXISTS idx_routes_enabled ON proxy_routes(enabled);

	CREATE TABLE IF NOT EXISTS users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS groups (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL UNIQUE,
		description TEXT DEFAULT '',
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS user_groups (
		user_id  INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
		group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
		PRIMARY KEY (user_id, group_id)
	);

	CREATE TABLE IF NOT EXISTS invites (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		code_hash  TEXT NOT NULL UNIQUE,
		used       BOOLEAN DEFAULT FALSE,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sessions (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		token_hash TEXT NOT NULL UNIQUE,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token_hash);
	`)
	if err != nil {
		return xerrors.Newf("init schema: %w", err)
	}
	slog.Info("database schema initialized")
	return nil
}

// migrate adds columns to existing databases that predate the current schema.
func (s *Storage) migrate() {
	for _, stmt := range []string{
		`ALTER TABLE proxy_routes ADD COLUMN allowed_groups  TEXT DEFAULT ''`,
		`ALTER TABLE proxy_routes ADD COLUMN cookie_policy   TEXT DEFAULT 'persistent'`,
		`ALTER TABLE proxy_routes ADD COLUMN renew_on_access BOOLEAN DEFAULT FALSE`,
	} {
		s.db.Exec(stmt) // intentionally ignore "column already exists" errors
	}
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
