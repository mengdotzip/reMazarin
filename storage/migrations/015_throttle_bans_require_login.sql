-- require_login lets a route demand any valid session (any signed-in user)
-- without restricting to a specific group — the "signed-in" access mode. It sits
-- alongside allowed_groups: empty groups + require_login = "any logged-in user".
ALTER TABLE proxy_routes ADD COLUMN require_login BOOLEAN NOT NULL DEFAULT FALSE;

-- Per-tier rate-limit and auto-ban policy. tier is 'anonymous', 'signed-in', or
-- 'group:<id>' for a per-group override. All policies are opt-in (disabled by
-- default) and managed at runtime from the admin panel.
CREATE TABLE IF NOT EXISTS throttle_policies (
    tier             TEXT    PRIMARY KEY,        -- 'anonymous' | 'signed-in' | 'group:<id>'
    enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    rate_per_sec     REAL    NOT NULL DEFAULT 0, -- token refill rate per IP
    burst            INTEGER NOT NULL DEFAULT 0, -- bucket capacity per IP
    ban_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    ban_threshold    INTEGER NOT NULL DEFAULT 0, -- failures within the window
    ban_window_sec   INTEGER NOT NULL DEFAULT 0, -- sliding failure window
    ban_duration_sec INTEGER NOT NULL DEFAULT 0  -- 0 = ban until manually cleared
);

-- Active and historical IP bans. expires_at NULL means the ban holds until an
-- admin clears it; a future timestamp auto-expires.
CREATE TABLE IF NOT EXISTS banned_ips (
    ip         TEXT PRIMARY KEY,
    reason     TEXT     DEFAULT '',
    tier       TEXT     DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_banned_ips_expires ON banned_ips(expires_at);

-- Seed the two built-in tiers, disabled by default — the operator opts in.
INSERT OR IGNORE INTO throttle_policies (tier, enabled) VALUES ('anonymous', FALSE);
INSERT OR IGNORE INTO throttle_policies (tier, enabled) VALUES ('signed-in', FALSE);
