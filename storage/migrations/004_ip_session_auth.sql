ALTER TABLE proxy_routes ADD COLUMN ip_auth BOOLEAN DEFAULT FALSE;
ALTER TABLE sessions     ADD COLUMN client_ip TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_sessions_ip ON sessions(client_ip);
