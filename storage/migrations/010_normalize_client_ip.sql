-- Older builds could persist sessions.client_ip with BLOB storage class instead
-- of TEXT. SQLite never treats a BLOB as equal to a TEXT literal, so IP session
-- auth (WHERE client_ip = ?) silently never matched those rows. Rewrite any
-- non-TEXT values to TEXT so existing sessions authenticate by IP again.
UPDATE sessions SET client_ip = CAST(client_ip AS TEXT) WHERE typeof(client_ip) <> 'text';
