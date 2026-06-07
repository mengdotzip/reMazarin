-- The per-route session_duration (005) and renew_on_access (002) were never
-- enforced: the proxy hot path and the extend endpoint always use the global
-- settings values. They only fed the auth page display, where they could even
-- disagree with what was actually enforced. Drop them; session duration and
-- renewal are global (settings table).
ALTER TABLE proxy_routes DROP COLUMN renew_on_access;
ALTER TABLE proxy_routes DROP COLUMN session_duration;
