-- The per-route cookie_policy (persistent/session/none) was a three-way TEXT
-- field that was never enforced. Replace it with a single boolean per-route
-- choice: persistent_login. When TRUE (the default) the route authenticates via
-- a persistent login cookie that survives browser restarts/idle — the DB session
-- (kept alive by access, including TCP) governs validity. When FALSE the cookie
-- is a session cookie the browser drops on close. This sits alongside ip_auth
-- (IP session auth) as the cookie-based auth option for HTTP group routes.
ALTER TABLE proxy_routes DROP COLUMN cookie_policy;
ALTER TABLE proxy_routes ADD COLUMN persistent_login BOOLEAN NOT NULL DEFAULT TRUE;
