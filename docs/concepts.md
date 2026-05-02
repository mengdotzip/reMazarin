# Concepts

## Access control and groups

Routes are **public** by default (no groups assigned). Assigning one or more groups to a route makes it private — only logged-in users who belong to at least one of those groups can access it.

The **admin** group is a system group and cannot be deleted. It is created automatically on first run together with the default `admin` user.

## The admin panel route

The admin panel host (configured under `[admin]` in `config.toml`) is automatically protected by the `admin` group at startup. This is applied once the first time the route is created; if you later change it via the admin panel, that setting is kept across restarts.

Unauthenticated requests to the admin host receive a plain HTTP 401 — the HTML is never sent to the browser.

## Dynamic route changes

Access control changes made through the admin panel take effect **immediately** — no restart required. The proxy keeps an in-memory cache of route access rules. Saving a route in the admin panel triggers an instant cache refresh. The cache also refreshes automatically every 5 minutes, which covers any manual database edits.

## Cookie policies

Each route can have an independent cookie policy that controls how long the session cookie lasts for that route.

| Policy | Behaviour |
|--------|-----------|
| **Persistent** | Cookie is stored on disk with a 7-day expiry. The browser keeps the user logged in across restarts. |
| **Session** | Cookie has no `Max-Age`. The browser discards it when the tab or window is closed. |
| **None** | No session cookie is issued or required. Use this only for truly public routes where you never want cookie overhead. |

**Renew on access** — when enabled, every successful request extends the session by another 7 days, so active users never get logged out.

## Registration and invites

New users can only register with a valid invite code generated in the admin panel. Invite codes are time-limited (default 24 hours) and can only be used once.
