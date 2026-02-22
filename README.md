> ⚠️ **WIP STATUS: source code for `ztnet-dns` is not published in this repository yet.**
> This repository is currently documentation-only. Planned source location: this same repository (`main` branch). Target publish window: **Q2 2026**.

> CoreDNS plugin for dynamic A/AAAA resolution of ZeroTier network members via ZTNET API.

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev)
[![CoreDNS](https://img.shields.io/badge/CoreDNS-1.11.x-blue)](https://coredns.io)
[![License: MIT](https://img.shields.io/badge/License-MIT-green)](LICENSE)

---

## Repository Status

- **Current state:** Documentation-only repository.
- **Build today:** Not available (no Go source files are currently published).
- **Test today:** Not available in this repository.
- **Run today:** Not available in this repository.
- **Planned source location / ETA:** `https://github.com/CleoWixom/ztnet-dns` (`main` branch), target publication in **Q2 2026**.

---

## What it does

`ztnet-dns` is a CoreDNS plugin that watches your [ZTNET](https://ztnet.network) API and
automatically serves DNS `A`/`AAAA` records for every authorized ZeroTier network member —
no `/etc/hosts` edits, no restarts, no cron jobs.

```
ping server01.zt.example.com   # resolves to ZT member IP automatically
ping efcc1b0947.zt.example.com # same node, resolved by ZT node ID
```

Records update every 30 seconds (configurable). Only clients **inside the ZT network**
can query the ZT zone — external requests receive `REFUSED`. Global internet resolution
(e.g. `google.com`) works normally for all ZT clients via `forward`.

---

## Architecture

```
ZT client → CoreDNS
            ├── query "server01.zt.example.com"  → ztnet plugin → A record from cache
            ├── query "google.com"                → forward → 1.1.1.1  (internet works)
            └── external query for ZT zone        → REFUSED (topology protected)

Background goroutine: ZTNET API → refresh cache every 30s (atomic swap, zero downtime)
```

### Key design decisions

| Decision | Reason |
|----------|--------|
| `atomic.Value` cache (not `sync.RWMutex`) | Wait-free reads in `ServeDNS` hot path |
| Source IP check **after** zone check | ZT clients need global DNS too — don't block `google.com` |
| `REFUSED` for out-of-zone requests to ZT zone | Hides network topology from external scanners |
| Token stored in file/env, not Corefile | Prevents accidental secret commits to git |
| ZT CIDR allowlist built from API routes | No manual IP management needed |
| Stale cache on API error | DNS keeps working if ZTNET is temporarily unreachable |

---

## Requirements

- Go 1.22+
- CoreDNS 1.11.x (requires rebuild with plugin)
- ZTNET instance with API access

---

## Installation

`ztnet-dns` is not installable from this repository yet because source code and build artifacts are not currently published.

For now, treat this README as product/design documentation. Installation steps will be added once source is published on `main` (target: Q2 2026).

---

## Configuration

### Corefile reference

```corefile
zt.example.com {
    ztnet {
        # ── Required ──────────────────────────────────────────
        api_url    http://ztnet.local:3000     # ZTNET instance URL
        network_id 8056c2e21c000001            # ZeroTier network ID
        zone       zt.example.com              # DNS zone to serve

        # ── Token (exactly one of these three) ────────────────
        token_file /run/secrets/ztnet_token    # RECOMMENDED: file (Docker/K8s secrets)
        # token_env  ZTNET_API_TOKEN           # Alternative: environment variable
        # api_token  dev-only-token            # Dev only — triggers WARNING in log

        # ── Access control ────────────────────────────────────
        auto_allow_zt    true                  # Allow ZT CIDR from network routes API
        allowed_networks 10.10.0.0/24          # Extra static CIDR (repeat per network)
        strict_start     false                 # If true: REFUSED until first refresh succeeds

        # ── Search domain ─────────────────────────────────────
        search_domain     zt.example.com       # Announced via DNS-SD TXT (RFC 6763)
        allow_short_names false                # true: "server01" → "server01.zt.example.com"

        # ── Tuning ────────────────────────────────────────────
        ttl         60    # Record TTL in seconds        (default: 60)
        refresh     30s   # API poll interval            (default: 30s)
        timeout     5s    # HTTP request timeout         (default: 5s)
        max_retries 3     # Retries on 5xx/timeout       (default: 3)
    }
    prometheus :9153
    log
    errors
}

# Global DNS for all ZT clients — internet keeps working
. {
    bind 10.147.20.1          # bind to ZT interface only (prevents open resolver)
    forward . 1.1.1.1 8.8.8.8
    cache
    log
    errors
}
```

### Parameter reference

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `api_url` | string | — | ZTNET API base URL |
| `network_id` | string | — | ZeroTier network ID |
| `zone` | string | — | DNS zone (FQDN) |
| `token_file` | string | — | Path to file containing API token ¹ |
| `token_env` | string | — | Env var name containing API token ¹ |
| `api_token` | string | — | Inline API token — dev only, logs WARNING ¹ |
| `auto_allow_zt` | bool | `true` | Build CIDR allowlist from ZT network routes |
| `allowed_networks` | []CIDR | `[]` | Extra static allowed CIDRs (repeat line per CIDR) |
| `strict_start` | bool | `false` | REFUSED until first successful API refresh |
| `search_domain` | string | = zone | Domain announced via DNS-SD (RFC 6763) |
| `allow_short_names` | bool | `false` | Resolve bare names: `host` → `host.zone` |
| `ttl` | uint32 | `60` | DNS record TTL in seconds |
| `refresh` | duration | `30s` | ZTNET API poll interval |
| `timeout` | duration | `5s` | HTTP client timeout |
| `max_retries` | int | `3` | Retry attempts on transient errors |

> ¹ Exactly one of `token_file`, `token_env`, `api_token` must be set.

---

## Token management

**Never put the token directly in Corefile** — it will end up in version control.

### Docker Secrets (recommended for Docker Compose)

```yaml
# docker-compose.yml
services:
  coredns:
    image: ghcr.io/cleoWixom/ztnet-dns:latest
    secrets:
      - ztnet_token
    volumes:
      - ./Corefile:/etc/coredns/Corefile:ro
    ports:
      - "53:53/udp"
      - "53:53/tcp"

secrets:
  ztnet_token:
    file: ./secrets/ztnet_token.txt   # chmod 600, not committed to git
```

```corefile
# Corefile
zt.example.com {
    ztnet {
        token_file /run/secrets/ztnet_token
        # ...
    }
}
```

The token file is re-read on every refresh cycle — rotate the secret without restarting CoreDNS.

### Kubernetes Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ztnet-token
type: Opaque
stringData:
  token: "your-api-token-here"
```

```yaml
# In your Pod spec:
volumes:
  - name: ztnet-token
    secret:
      secretName: ztnet-token
      defaultMode: 0400
volumeMounts:
  - name: ztnet-token
    mountPath: /run/secrets
    readOnly: true
```

### systemd (bare metal)

```ini
# /etc/coredns/coredns.env
ZTNET_API_TOKEN=your-api-token-here
```

```ini
# /etc/systemd/system/coredns.service
[Service]
EnvironmentFile=/etc/coredns/coredns.env
ExecStart=/usr/local/bin/coredns -conf /etc/coredns/Corefile
```

```corefile
# Corefile
ztnet {
    token_env ZTNET_API_TOKEN
}
```

---

## How ZT clients get DNS + search domain

ZT clients need to know two things: which DNS server to use, and what search domain to append
for short names. Three ways to configure this:

### Option 1 — ZeroTier network DNS settings (recommended)

Configure DNS in ZTNET UI or via API — ZeroTier pushes it to all clients automatically:

```json
{
  "dns": {
    "domain": "zt.example.com",
    "servers": ["10.147.20.1"]
  }
}
```

No manual client configuration needed. Works on all platforms where ZeroTier manages DNS.

### Option 2 — `/etc/resolv.conf` (Linux, manual)

```
nameserver 10.147.20.1
search     zt.example.com
options    ndots:1
```

### Option 3 — DHCP option 119 (network-wide)

```
# ISC dhcpd
option domain-search "zt.example.com";
option domain-name-servers 10.147.20.1;
```

---

## Record naming

For each authorized ZT member, the plugin creates two DNS names:

| Name pattern | Example | Resolves to |
|---|---|---|
| `{member_name}.{zone}` | `server01.zt.example.com` | Member's ZT IP(s) |
| `{node_id}.{zone}` | `efcc1b0947.zt.example.com` | Same IP(s), by ZT node ID |

Spaces in member names are replaced with underscores (`my server` → `my_server`).

IPv6 addresses (RFC4193, 6plane) are served as `AAAA` records automatically when enabled
in the ZeroTier network settings.

---

## Security model

```
External client → query for zt.example.com → REFUSED  (topology hidden)
ZT client       → query for zt.example.com → answered (from cache)
ZT client       → query for google.com     → forwarded to upstream DNS (internet works)
```

### Access control

The plugin builds a CIDR allowlist from:
1. **ZT network routes** — fetched automatically from ZTNET API (`via == null` routes only)
2. **Static CIDRs** — `allowed_networks` in Corefile
3. **Loopback** — `127.0.0.0/8`, `::1/128` — always allowed, cannot be disabled

The allowlist is updated atomically with the DNS record cache on every refresh cycle.
Stale allowlist is preserved on API errors — no sudden lockouts.

### Important: this plugin does NOT protect global DNS

Only queries for the ZT zone (`zt.example.com`) are access-controlled.
If CoreDNS listens on a public interface, the `forward` block is still an open resolver.
**Use `bind` to listen only on the ZT interface**, or restrict with firewall rules.

---

## Metrics (Prometheus)

Available at `:9153/metrics` when `prometheus` plugin is enabled:

| Metric | Labels | Description |
|--------|--------|-------------|
| `coredns_ztnet_requests_total` | `zone`, `rcode` | DNS requests handled |
| `coredns_ztnet_refused_total` | `zone` | Requests refused (not from ZT network) |
| `coredns_ztnet_cache_refresh_total` | `zone`, `status` | API refresh attempts |
| `coredns_ztnet_cache_entries` | `zone`, `type` | Current A/AAAA entries in cache |
| `coredns_ztnet_token_reload_total` | `zone`, `source`, `status` | Token reload attempts |

---

## DNS record flow

```
ZTNET API ──refresh──► Members + NetworkInfo
                              │
                    filter: authorized == true
                              │
                    build: A map + AAAA map
                    (IPv4 from ipAssignments,
                     IPv6 from RFC4193/6plane)
                              │
                    atomic.Store(new snapshot)
                              │
CoreDNS ServeDNS ──read──► atomic.Load(snapshot)
                              │
                    zone check → source IP check → cache lookup → response
```

---

## Troubleshooting

### ZT clients can't resolve `google.com`

Check that your Corefile has a `.` zone block with `forward`:
```corefile
. {
    bind 10.147.20.1
    forward . 1.1.1.1 8.8.8.8
    cache
}
```
Without this, the `ztnet` plugin is the last handler and returns `NXDOMAIN` for everything outside its zone.

### Getting `REFUSED` from inside the ZT network

1. Check that your ZT IP is in the network's route table:
   ```bash
   curl -sH "x-ztnet-auth: $TOKEN" http://ztnet.local:3000/api/v1/network/$NETWORK_ID/ \
     | jq '.routes'
   ```
2. Ensure `auto_allow_zt: true` (default) or add your CIDR to `allowed_networks`.
3. Check logs: `REFUSED query from X.X.X.X — not a ZT member`.

### Records not updating

Check the refresh logs:
```bash
docker logs coredns 2>&1 | grep ztnet
```
Common causes: API token expired (check `token_reload_total` metric), ZTNET unreachable
(plugin serves stale cache — check `cache_refresh_total{status="error"}`).

### Member names with special characters

Spaces → underscores. Other non-DNS characters in member names are preserved as-is but may
cause resolution issues. Keep member names DNS-safe: `[a-z0-9-_]+`.

---

## Development

Development, local builds, tests, and linting are **not runnable yet** from this repository because the implementation files are not published.

When source is published, this section will include the canonical `go test`, `go vet`, and `golangci-lint` commands.

---

## Contributing

See [AGENTS.md](AGENTS.md) for development conventions, code structure, and how to add features.

Pull requests welcome. Please run tests and linter before submitting.

---

## License

MIT © CleoWixom
