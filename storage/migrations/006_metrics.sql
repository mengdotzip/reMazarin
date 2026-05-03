CREATE TABLE auth_failures (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    ip         TEXT     NOT NULL,
    username   TEXT     NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_auth_failures_created ON auth_failures(created_at);
