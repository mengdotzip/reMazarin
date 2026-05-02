CREATE TABLE IF NOT EXISTS proxy_routes (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    url           TEXT    NOT NULL UNIQUE,
    target        TEXT    NOT NULL,
    type          TEXT    DEFAULT 'proxy',
    tls           BOOLEAN DEFAULT FALSE,
    cert          TEXT    DEFAULT '',
    key           TEXT    DEFAULT '',
    enabled       BOOLEAN DEFAULT TRUE,
    source        TEXT    NOT NULL,
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
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE,
    description TEXT    DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_groups (
    user_id  INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

CREATE TABLE IF NOT EXISTS invites (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code_hash  TEXT    NOT NULL UNIQUE,
    used       BOOLEAN DEFAULT FALSE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash TEXT    NOT NULL UNIQUE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token_hash);
