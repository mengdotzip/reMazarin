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
```

On first run the database is seeded with:
- **Username:** `admin`  **Password:** `admin123`  ← change this immediately

## Admin panel

| Section | What you can do |
|---------|----------------|
| Users   | View users, assign/remove group membership, delete users |
| Groups  | Create and delete groups |
| Invites | Generate invite codes (time-limited) for new user registration |
| Routes  | Set allowed groups per route, configure cookie lifetime |

Routes with no groups assigned are **public**. Routes with one or more groups require the visitor to be logged in and a member of at least one of those groups.

Access control changes take effect **immediately** — no restart needed. See [docs/concepts.md](docs/concepts.md) for details on cookie policies, the admin group, and how the route cache works.

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

## Registration

New users register at `<web-host>/register` using an invite code generated in the admin panel.
