# reMazarin

A self-hosted reverse proxy with built-in authentication, group-based access control, and an admin panel.

## How it works

reMazarin sits in front of your services. Routes are defined in `config.toml` and access control is managed entirely through the admin panel — no config restarts needed for auth changes.

- **Login page** — served from the `[web]` host. Shows accessible routes after sign-in.
- **Admin panel** — served from the `[admin]` host. Requires the `admin` group.
- **Proxy routes** — forward traffic to backends. Optional per-route group restrictions.

## Setup

```toml
database = "./remazarin.db"

[web]
enabled = true
url     = "auth.example.com:443"
tls     = true
cert    = "./certs/cert.pem"
key     = "./certs/key.pem"

[admin]
enabled = true
url     = "admin.example.com:443"
tls     = true
cert    = "./certs/cert.pem"
key     = "./certs/key.pem"

[[routes]]
url    = "app.example.com:443"
target = "localhost:8000"
tls    = true
cert   = "./certs/cert.pem"
key    = "./certs/key.pem"

# TCP passthrough route (raw port forwarding, no TLS termination)
[[routes]]
url    = "myhost.example.com:2222"
target = "localhost:22"
type   = "tcp"
```

See [docs/deployment.md](docs/deployment.md) for running reMazarin as a hardened systemd service with a dedicated user.

On first run the database is seeded with:
- **Username:** `admin`  **Password:** `admin123`  ← change this immediately

## Admin panel

| Section | What you can do |
|---------|----------------|
| Users   | View users, assign/remove group membership, delete users |
| Groups  | Create and delete groups |
| Invites | Generate invite codes with an optional description and configurable expiry (default 24 h) |
| Routes  | Set allowed groups and/or IP allowlists per route, configure cookie lifetime and session duration |
| Metrics | Live view of active sessions, per-route request counts, access log (last 200 events), and login failures |

Routes with no groups and no IPs assigned are **public**. Routes can be restricted by:

- **Group membership** — visitor must be logged in and a member of at least one allowed group.
- **IP allowlist** — a matching client IP grants access without a session (plain IPs or CIDR ranges, comma-separated). For TCP routes, non-matching IPs have their connection closed immediately.

Access control changes take effect **immediately** — no restart needed. See [docs/concepts.md](docs/concepts.md) for details on cookie policies, IP session auth, the admin group, and how the route cache works.

## Custom API handlers

Add your own API functions in `api/api.go` inside `InitApi()`:

```go
if err := register("myfunction", HandleMyFunction); err != nil {
    return err
}
```

Then call it via `/api/myfunction` from any static-served page, or point a route to it:

```toml
[[routes]]
url    = "api.example.com:443"
target = "myfunction"
type   = "api"
```

## Database migrations

Schema changes are managed through numbered SQL files in `storage/migrations/`, embedded into the binary at compile time. On startup, reMazarin automatically applies any unapplied migrations. Existing databases are bootstrapped transparently — no manual steps needed when upgrading.

See [docs/migrations.md](docs/migrations.md) for the migration history and instructions for adding new migrations.

## Registration

New users register at `<web-host>/register` using an invite code generated in the admin panel.

## Session management

After signing in, the login page shows a **Sessions** sidebar listing all active sessions for the current user (IP address, expiry). Any session can be revoked individually — useful if you logged in on a mobile connection and forgot to sign out. Revoking the current session logs you out immediately.
