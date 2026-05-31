CREATE TABLE IF NOT EXISTS settings (
    id                    INTEGER PRIMARY KEY CHECK (id = 1),
    session_duration_hours INTEGER NOT NULL DEFAULT 168,
    renew_on_access        BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT OR IGNORE INTO settings (id, session_duration_hours, renew_on_access) VALUES (1, 168, TRUE);
