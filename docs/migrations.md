# Database migrations

reMazarin uses a simple numbered-migration system. Migration SQL files live in `storage/migrations/` and are embedded directly into the binary — no separate files are needed at runtime.

## How it works

On every startup, the runner:

1. Creates a `schema_migrations` table if it doesn't exist (the only hardcoded SQL).
2. **Bootstraps** existing databases — if no migrations are recorded yet but tables already exist, it inspects column presence to infer which migrations are already applied and marks them without re-running them.
3. Runs any unapplied migrations in version order, then records them in `schema_migrations`.

Each migration is a plain `.sql` file. The runner splits it into individual statements and executes them one by one, correctly handling SQLite trigger bodies (`BEGIN...END` blocks) that contain internal semicolons.

## Adding a migration

1. Create `storage/migrations/NNN_description.sql` where `NNN` is the next unused three-digit number.
2. Write standard SQLite DDL or DML. Each statement must end with `;`.
3. Rebuild — the file is embedded at compile time.

```sql
-- storage/migrations/005_example.sql
ALTER TABLE proxy_routes ADD COLUMN my_new_column TEXT DEFAULT '';
```

That's it. The runner will apply it on the next startup and record it in `schema_migrations`.

## Migration history

| Version | File | What it adds |
|---------|------|-------------|
| 001 | `001_initial_schema.sql` | Core tables: `proxy_routes`, `users`, `groups`, `user_groups`, `invites`, `sessions` |
| 002 | `002_access_control.sql` | `allowed_groups`, `cookie_policy` (dropped in 012), `renew_on_access` on `proxy_routes` |
| 003 | `003_allowed_ips.sql` | `allowed_ips` on `proxy_routes` |
| 004 | `004_ip_session_auth.sql` | `ip_auth` on `proxy_routes`; `client_ip` on `sessions` |
| 012 | `012_cookie_settings.sql` | Replaces the never-enforced three-way `cookie_policy` on `proxy_routes` with a boolean `persistent_login` (default true): per-route gate for cookie auth |
| 013 | `013_drop_route_session_fields.sql` | Drops the never-enforced per-route `renew_on_access` and `session_duration` from `proxy_routes` (both are global, in `settings`) |
| 014 | `014_route_range_group.sql` | `range_group` on `proxy_routes` — links the ports of a port-range route |
| 015 | `015_throttle_bans_require_login.sql` | `require_login` on `proxy_routes` (the "signed-in" access mode); `throttle_policies` (per-tier rate-limit + auto-ban config) and `banned_ips` tables |

## Existing databases

Databases created before the migration system existed (those that used the old `initSchema`+`migrate()` approach) are handled transparently. On first run with the new code, the bootstrap step checks which columns already exist and marks the corresponding migrations as applied — so no data is lost and no statement is run twice.
