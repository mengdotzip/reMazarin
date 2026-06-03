# Deployment (systemd)

This guide covers running reMazarin as a hardened systemd service on Linux with a dedicated unprivileged user that can still bind to ports 80 and 443.

---

## 1. Create a dedicated system user

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin remazarin
```

A system account (`--system`) with no login shell keeps the attack surface small. The user will own only the files it needs.

---

## 2. Set up the directory layout

```bash
sudo mkdir -p /opt/remazarin/certs
sudo cp remazarin /usr/local/bin/remazarin
sudo chmod 755 /usr/local/bin/remazarin

# Config, database, and certs live under /opt/remazarin
sudo cp config.toml /opt/remazarin/config.toml
sudo cp certs/cert.pem certs/key.pem /opt/remazarin/certs/

# The remazarin user owns the working directory so it can write the database
sudo chown -R remazarin:remazarin /opt/remazarin
sudo chmod 750 /opt/remazarin

# Private key must not be world-readable
sudo chmod 640 /opt/remazarin/certs/key.pem
```

Update `config.toml` to use absolute paths:

```toml
database = "/opt/remazarin/remazarin.db"

[web]
cert = "/opt/remazarin/certs/cert.pem"
key  = "/opt/remazarin/certs/key.pem"
```

---

## 3. Install the systemd unit

Save the following to `/etc/systemd/system/remazarin.service`:

```ini
[Unit]
Description=reMazarin Proxy Server
Documentation=https://github.com/mengdotzip/remazarin
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=remazarin
Group=remazarin
WorkingDirectory=/opt/remazarin
ExecStart=/usr/local/bin/remazarin
Restart=on-failure
RestartSec=6s
StandardOutput=journal
StandardError=journal

# Allow binding to privileged ports (80, 443) without running as root
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/opt/remazarin
ProtectHome=true

LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

**Key settings explained:**

| Setting | Purpose |
|---|---|
| `User=remazarin` | Run as the unprivileged system user |
| `AmbientCapabilities=CAP_NET_BIND_SERVICE` | Lets the process bind ports < 1024 (80, 443) without root |
| `CapabilityBoundingSet=CAP_NET_BIND_SERVICE` | Prevents acquiring any other capability at runtime |
| `NoNewPrivileges=true` | Blocks privilege escalation via setuid/setgid |
| `PrivateTmp=true` | Isolated `/tmp` — other processes cannot read temp files |
| `ProtectSystem=strict` | Mounts `/usr`, `/boot`, `/etc` read-only |
| `ReadWritePaths=/opt/remazarin` | Explicitly allows writes to the data directory |
| `ProtectHome=true` | `/home`, `/root` are invisible to the process |

---

## 4. Enable and start

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now remazarin
```

Check status and logs:

```bash
sudo systemctl status remazarin
sudo journalctl -u remazarin -f
```

---

## 5. TLS certificate renewals

If you use Let's Encrypt (e.g. via Certbot), the `remazarin` user needs read access to the renewed certs, or a post-renewal hook can copy them and restart the service:

```bash
# /etc/letsencrypt/renewal-hooks/deploy/remazarin.sh
#!/bin/bash
cp /etc/letsencrypt/live/example.com/fullchain.pem /opt/remazarin/certs/cert.pem
cp /etc/letsencrypt/live/example.com/privkey.pem   /opt/remazarin/certs/key.pem
chown remazarin:remazarin /opt/remazarin/certs/*.pem
chmod 640 /opt/remazarin/certs/key.pem
systemctl restart remazarin
```

```bash
sudo chmod +x /etc/letsencrypt/renewal-hooks/deploy/remazarin.sh
```
