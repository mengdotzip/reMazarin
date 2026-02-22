package storage

import (
	"context"
	"database/sql"
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

	return s, nil
}

func (s *Storage) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS proxy_routes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		url TEXT NOT NULL UNIQUE,
		target TEXT NOT NULL,
		type TEXT DEFAULT 'proxy',
		tls BOOLEAN DEFAULT FALSE,
		cert TEXT,
		key TEXT,
		enabled BOOLEAN DEFAULT TRUE,
		source TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_path ON proxy_routes(url);
	CREATE INDEX IF NOT EXISTS idx_enabled ON proxy_routes(enabled);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return xerrors.Newf("create schema: %w", err)
	}

	slog.Info("database schema initialized")
	return nil
}

func (s *Storage) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Storage) Close() error {
	slog.Info("closing database connection")
	return s.db.Close()
}
