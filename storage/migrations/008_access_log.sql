CREATE TABLE access_log (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    ip         TEXT     NOT NULL,
    username   TEXT     NOT NULL DEFAULT '',
    route_url  TEXT     NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_access_log_created ON access_log(created_at);
