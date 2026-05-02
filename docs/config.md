# config.toml reference

This document covers every option available in `config.toml`. Access-control settings (allowed groups, IP allowlists, cookie policy, renew-on-access) are managed through the admin panel at runtime and are **not** part of `config.toml`.

---

## Top-level

```toml
database = "./remazarin.db"
```

| Key        | Type   | Default              | Description                              |
|------------|--------|----------------------|------------------------------------------|
| `database` | string | `"./remazarin.db"`   | Path to the SQLite database file. The directory must already exist. |

---

## `[web]`

The login / user-facing web UI. Serves the login page, registration page, and the post-login route list.

```toml
[web]
enabled = true
url     = "auth.example.com:443"
tls     = true
cert    = "./certs/cert.pem"
key     = "./certs/key.pem"
```

| Key       | Type    | Default | Description                                                       |
|-----------|---------|---------|-------------------------------------------------------------------|
| `enabled` | bool    | `true`  | Set to `false` to disable the web UI entirely.                    |
| `url`     | string  | —       | `host:port` the web UI listens on. Required when `enabled = true`.|
| `tls`     | bool    | `false` | Enable TLS on this listener.                                      |
| `cert`    | string  | `""`    | Path to the TLS certificate file. Required when `tls = true`.     |
| `key`     | string  | `""`    | Path to the TLS private key file. Required when `tls = true`.     |

---

## `[admin]`

The admin panel. Requires the `admin` group for all API calls.

```toml
[admin]
enabled = true
url     = "admin.example.com:443"
tls     = true
cert    = "./certs/cert.pem"
key     = "./certs/key.pem"
```

| Key       | Type    | Default | Description                                                          |
|-----------|---------|---------|----------------------------------------------------------------------|
| `enabled` | bool    | `true`  | Set to `false` to disable the admin panel entirely.                  |
| `url`     | string  | —       | `host:port` the admin panel listens on. Required when `enabled = true`. |
| `tls`     | bool    | `false` | Enable TLS on this listener.                                         |
| `cert`    | string  | `""`    | Path to the TLS certificate file. Required when `tls = true`.        |
| `key`     | string  | `""`    | Path to the TLS private key file. Required when `tls = true`.        |

---

## `[otel]`

OpenTelemetry tracing integration. When enabled, HTTP handlers are wrapped with `otelhttp`.

```toml
[otel]
enabled          = false
endpoint         = "localhost:4317"
interval         = 15
runtime_interval = 30
```

| Key               | Type    | Default | Description                                                                      |
|-------------------|---------|---------|----------------------------------------------------------------------------------|
| `enabled`         | bool    | `false` | Enable OpenTelemetry tracing and metrics.                                        |
| `endpoint`        | string  | `""`    | OTLP gRPC exporter endpoint (`host:port`, e.g. `localhost:4317`). Required when `enabled = true`. |
| `interval`        | int     | `15`    | Metric export interval in seconds. Also controls the trace batch flush interval. |
| `runtime_interval`| int     | `30`    | How often Go runtime memstats (GC, heap, goroutines) are read, in seconds.       |

---

## `[[routes]]`

Each `[[routes]]` block defines one proxy route. Multiple blocks are allowed.

```toml
[[routes]]
url    = "app.example.com:443"
target = "localhost:8000"
type   = "proxy"
tls    = true
cert   = "./certs/cert.pem"
key    = "./certs/key.pem"
```

| Key      | Type   | Default   | Description                                                                |
|----------|--------|-----------|----------------------------------------------------------------------------|
| `url`    | string | —         | `host:port` this route matches on. Required. Must be unique.               |
| `target` | string | —         | Backend address or identifier. Required. See route types below.            |
| `type`   | string | `"proxy"` | Route type. One of `proxy`, `static`, `api`, or `tcp`.                    |
| `tls`    | bool   | `false`   | Terminate TLS on the listener for this route's port.                       |
| `cert`   | string | `""`      | Path to the TLS certificate file. Required when `tls = true`.              |
| `key`    | string | `""`      | Path to the TLS private key file. Required when `tls = true`.              |

### Route types

| Type     | `target` value              | Description                                                                  |
|----------|-----------------------------|------------------------------------------------------------------------------|
| `proxy`  | `host:port` or URL          | Reverse-proxy HTTP/HTTPS traffic to the target backend.                      |
| `static` | filesystem path             | Serve a directory of static files from the given path (e.g. `./www/myapp`). |
| `api`    | registered function name    | Route to a built-in Go API handler registered in `api/api.go`.               |
| `tcp`    | `host:port`                 | Raw TCP passthrough — no HTTP parsing, no TLS termination.                   |

### Notes on TLS

All routes on the same port share one listener. TLS configuration (cert/key) must be identical for every route on a given port — you cannot mix TLS and non-TLS routes on the same port.

### Access control

Access-control settings (allowed groups, IP allowlists, cookie policy, renew-on-access) are **not** part of `config.toml`. They are managed at runtime through the admin panel and stored in the database. Changes take effect immediately without a restart.

See [concepts.md](concepts.md) for details on how group auth and IP-based auth interact.
