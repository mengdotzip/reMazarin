# Concepts

## Access control and groups

Routes are **public** by default (no groups assigned). Assigning one or more groups to a route makes it private — only logged-in users who belong to at least one of those groups can access it.

The **admin** group is a system group and cannot be deleted. It is created automatically on first run together with the default `admin` user.

### The "signed-in" access mode

Between fully public and group-restricted there is a third option: **Require login**. A
route with require-login set accepts **any signed-in user** — a valid session is enough, no
specific group membership is needed. This is the right setting for something every account
should reach but anonymous visitors should not. It works through cookie auth (and IP session
auth) exactly like group auth, but skips the group-membership check. In the admin panel a
require-login route shows a **signed-in** badge instead of **public**.

## The admin panel route

The admin panel host (configured under `[admin]` in `config.toml`) is automatically protected by the `admin` group at startup. This is applied once the first time the route is created; if you later change it via the admin panel, that setting is kept across restarts.

Unauthenticated requests to the admin host receive a plain HTTP 401 — the HTML is never sent to the browser.

## Dynamic route changes

Access control changes made through the admin panel take effect **immediately** — no restart required. The proxy keeps an in-memory cache of route access rules. Saving a route in the admin panel triggers an instant cache refresh. The cache also refreshes automatically every 5 minutes, which covers any manual database edits.

## Raw routes: TCP, UDP, and tcp+udp

Besides HTTP `proxy` routes, reMazarin can forward raw L4 traffic:

- **`tcp`** — accepts connections and pipes bytes to the target.
- **`udp`** — a NAT-style relay. UDP is connectionless, so for each client source
  address reMazarin dials a dedicated socket to the target and pumps replies back;
  flows idle for 120 s are reaped. Needed for things like coturn (TURN/STUN).
- **`tcp+udp`** — a single route that binds **both** a TCP and a UDP listener on the
  same port. This is one row in the admin panel; access control and the backend
  apply to both protocols. coturn's signalling port (`3478`) is the canonical case.

A raw TCP listener and an HTTP route cannot share a port (both bind TCP), but a UDP
listener is independent and coexists with either — which is what makes `tcp+udp`
on one port possible.

**Auth for raw routes is IP-based only.** There is no cookie/HTTP login over raw
TCP/UDP, so access is gated by **IP session auth** and/or the **static IP
allowlist** (see below). As with TCP, selecting allowed groups on a UDP or tcp+udp
route implies IP session auth — otherwise a group-restricted route would fail open.

**UDP and source IP:** a userspace UDP relay means the target sees *reMazarin's*
address as the source, not the real client's (each client still gets a distinct
relay source port, so the target's 5-tuple demux still works). Keep this in mind
when the backend does its own IP-based logic.

## Port-range routes

When creating a route in the admin panel you can tick **Port range** and give an
end port. The route is expanded into one route per port across the range
(`host:2000`, `host:2001`, … `host:2010`) — each port needs its own listener, so
a range cannot be a single socket. Ranges are supported for both `proxy` and
`tcp` routes and are capped at **256 ports** to avoid exhausting sockets/file
descriptors.

**Target mapping** is controlled by the **Offset target port** checkbox:

| Offset | Behaviour |
|--------|-----------|
| **On** | The target port walks alongside the listen port, starting from the target you entered: `:2000→host:3000`, `:2001→host:3001`, … This is the usual choice for TCP passthrough (passive FTP, RTP media, game servers). |
| **Off** | Every port in the range forwards to the same single target. |

All ports of a range share a hidden `range_group` id. The admin panel collapses
them into a **single row** displayed as `host:2000–2010`, and editing access
control or deleting the route applies to the whole range at once. Per-port
backend edits are not offered for a range (the offset makes a single target
ambiguous); access control (groups, IP allowlist, IP session auth) is identical
across every port.

If any port in the range conflicts with an existing route, the whole create is
rejected and nothing is added — there are no partial ranges.

## Cookie policies

Each route can have an independent cookie policy that controls how the session cookie behaves.

| Policy | Behaviour |
|--------|-----------|
| **Persistent** | Cookie is stored on disk and the browser keeps the user logged in across restarts. Expiry is set by **Session duration**. |
| **Session** | Cookie has no `Max-Age`. The browser discards it when the tab or window is closed. |
| **None** | No session cookie is issued or required. Use this only for truly public routes where you never want cookie overhead. |

**Renew on access** — when enabled, every successful request extends the session by another full session duration, so active users never get logged out.

## Session duration

Each route has a configurable **session duration** (in hours, default 168 = 7 days). This controls:

- **At login** — the auth/web route's session duration sets how long the initial session lasts and the cookie's `Max-Age`.
- **On renewal** — when renew on access is enabled, each successful request extends the session by the route's session duration.
- **IP session auth** — renewal via IP auth also uses the route's session duration.

## IP session auth

Enabling **IP session auth** on a route lets the connecting IP address act as the credential — no session cookie required. When a user logs into the web portal, their IP is stored with the session. Any subsequent connection from that same IP to a route with IP session auth enabled is automatically granted access.

This is the primary auth mechanism for TCP routes (SSH, etc.) since raw TCP cannot carry cookies.

**Auth precedence** (first match wins):

| Check | Condition |
|---|---|
| 1. IP session auth | `ip_auth` enabled and connecting IP has an active login session |
| 2. Static IP allowlist | `allowed_ips` is set and the IP matches |
| 3. Cookie auth | `allowed_groups` is set and the request carries a valid session cookie |

If none match, the request/connection is rejected.

**Group restriction with IP session auth** — if `allowed_groups` is also configured, the session found by IP must belong to a user in one of those groups. This lets you restrict IP session auth to specific teams (e.g. only users in the `devs` group can SSH through the TCP proxy).

**Static IP allowlist** (`allowed_ips`) is a separate, simpler mechanism: specific IPs or CIDR ranges that are always allowed regardless of whether they have an active session. Useful for trusted servers or CI/CD runners.

### Session lifetime and cleanup

The IP association lives for as long as the underlying session is valid. The duration is set by the auth/web route's **Session duration** (default 168 hours = 7 days). There is no separate IP-specific timer.

Sessions are cleaned up automatically every 5 minutes. Once a session expires, the associated IP immediately loses access to any IP-auth-protected route on the next connection attempt (TCP) or request (HTTP).

**Logout** — when the user logs out via the web portal, the session is deleted immediately. This revokes access from their IP address right away; there is no grace period.

**Multiple devices / IPs** — each login creates an independent session. A user who logs in from two different IP addresses has two sessions. Each session tracks its own IP and expiry independently.

### Renew on access

The **Renew on access** toggle works with IP session auth. When a connection authenticates via IP session auth and the route has renew on access enabled, the session expiry is extended by 7 days — the same behaviour as cookie auth. For TCP routes, renewal happens once per new connection (not once per packet). For HTTP routes, it happens on every successfully authenticated request.

This means a user who regularly SSHs through an IP-auth TCP route never gets logged out, as long as they connect within the session duration of their last access.

**Note on proxied deployments** — both mechanisms use the direct TCP connection's remote address. If reMazarin sits behind another load balancer or proxy, the IP seen is that proxy's address, not the end user's.

## Registration and invites

New users can only register with a valid invite code generated in the admin panel. Each invite has an optional description (so you can track who it was meant for) and a configurable expiry (default 24 hours). Invite codes are single-use and are cleaned up automatically once expired.

## Metrics

The admin panel **Metrics** tab provides a live view of:

- **Active Sessions** — every currently logged-in user with their IP, sign-in time, and session expiry. Admins can force-revoke any session.
- **Route Activity** — per-route *served* request counts since the last process start (in-memory, resets on restart).
- **Access Log** — the last 200 authorized access events: which user/IP accessed which route and when. API calls (`/api/*`) are excluded to reduce noise. TCP connections are also captured.
- **Login Failures** — recent failed login attempts with the attempted username and source IP.
- **Events** — *every* connection the proxy sees, not just the authorized happy path: per-outcome counters plus a recent-events ring (IP, route, outcome). Outcomes include `served`, `denied`, `rate_limited`, `banned`, `not_found` (unknown Host), `no_listener` (unknown port), `tls_error` (failed TLS handshake — plain HTTP to a TLS port, junk bytes, scans), `tcp_rejected`, and `dial_error`.
- **Banned IPs** — currently banned source IPs with reason and expiry. Admins can ban an IP manually or lift any ban here.

The events feed is **in-memory only** — per-outcome counters (unbounded) and a fixed-size ring
buffer of the most recent events. Junk/scan packets are deliberately *not* written to the DB
access log: a row per packet would turn a flood into a disk-write flood. The DB access log keeps
storing **authorized** events only.

Users can view and revoke their own sessions from the login page sidebar.

## Throttling and auto-ban

reMazarin can rate-limit and automatically ban abusive clients. Both are configured from the
admin **Throttle** tab and are **off by default** — the operator opts in.

### Tiers

Policies are keyed by access **tier**, not by route. An IP is classified by how it authenticates:

| Tier | Who |
|------|-----|
| **anonymous** | no valid session — the default for any unseen IP |
| **signed-in** | any authenticated user |
| **group:&lt;id&gt;** | an optional per-group override for members of a specific group |

A fresh/unseen IP starts at **anonymous** (the strictest tier) and is promoted to signed-in (or
a matching group override) once a request from it authenticates. The most specific enabled policy
wins: a per-group override beats signed-in, which beats anonymous. This is what lets you put a
tight limit on the login page — all unauthenticated traffic — without throttling logged-in users.

### Rate limit

Each policy is a per-IP **token bucket**: `rate_per_sec` tokens refill per second up to a
`burst` ceiling. A request with no token available gets **429 Too Many Requests** with a
`Retry-After` header. A disabled policy or a zero rate means unlimited.

### Auto-ban

A policy can also auto-ban: when an IP accumulates `ban_threshold` **failures** within a sliding
`ban_window_sec`, it is banned for `ban_duration_sec` (0 = until an admin clears it). A failure is
any denied/rate-limited/rejected event or a TLS handshake error from that IP — so the canonical
case "1000 failed requests in 10 minutes while still not signed in → ban" is just the anonymous
tier with `ban_threshold = 1000`, `ban_window_sec = 600`.

A banned IP is dropped at the **cheapest, earliest** gate — before any auth or backend work — on
both HTTP (403) and raw TCP/UDP (connection closed / packet dropped).

### Persistence

Bans are stored in the `banned_ips` table and **survive restarts** — they are reloaded into
memory at startup and on every cache refresh (immediately after an admin change, and on the 5-min
tick). Expired bans are cleaned up automatically. Rate-limit bucket state and the in-memory tier
classification are not persisted (they rebuild from traffic).
