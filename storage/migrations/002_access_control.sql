ALTER TABLE proxy_routes ADD COLUMN allowed_groups  TEXT    DEFAULT '';
ALTER TABLE proxy_routes ADD COLUMN cookie_policy   TEXT    DEFAULT 'persistent';
ALTER TABLE proxy_routes ADD COLUMN renew_on_access BOOLEAN DEFAULT FALSE;
