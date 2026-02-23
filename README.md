# ztnet-dns

CoreDNS external plugin for serving A/AAAA records of **authorized ZeroTier members** from ZTNET API.

## Production readiness status

- ✅ Core behavior is implemented for production use (zone-first routing, stale-on-error cache, atomic allowlist updates).
- ✅ Vulnerable CoreDNS line (`v1.11.x`) was replaced with `github.com/coredns/coredns v1.14.0`.
- ✅ Security scan can be clean when using a patched Go toolchain (validated with Go `1.25.7`).

## What the plugin does

- Serves DNS only for configured plugin zone (`zone`).
- For out-of-zone queries, forwards request to next CoreDNS plugin (does not block global DNS).
- Applies source-IP access control only for plugin zone using allowlist from:
  - static `allowed_networks`
  - optional ZeroTier routes (`auto_allow_zt true`, only routes with `via == nil`)
  - always includes loopback ranges `127.0.0.0/8` and `::1/128`
- Periodically refreshes records and allowlist from API; on refresh errors keeps last good snapshot (stale-on-error).
- Supports token sources: `token_file`, `token_env`, `api_token` (exactly one).
- Supports short names (`allow_short_names`) and DNS-SD TXT (`_dns-sd._udp.<zone>` with `path=<search_domain>`).

## Requirements

- Go `1.24+` (for vulnerability remediation use patched releases, e.g. `1.24.13+` or `1.25.7+`).
- CoreDNS with plugin compiled in (external plugin workflow).
- Reachable ZTNET API.

## Configuration

### Corefile example

```corefile
ztnet.local {
    bind 192.168.55.1
    ztnet {
        api_url http://127.0.0.1:3000
        network_id 8056c2e21c000001
        zone ztnet.local
        token_file /run/secrets/ztnet_token
        auto_allow_zt true
        refresh 30s
        timeout 5s
        ttl 60
    }
    prometheus :9153
    errors
    log
}

. {
    bind 192.168.55.1
    hosts {
        192.168.55.1 ztnet.local
        fallthrough
    }
    forward . 1.1.1.1 8.8.8.8
    cache
}
```

### Parameters

| Parameter | Required | Default | Description |
|---|---|---|---|
| `api_url` | yes | — | Base URL of ZTNET API |
| `network_id` | yes | — | ZeroTier network ID |
| `zone` | yes | — | Zone served by plugin |
| `token_file` / `token_env` / `api_token` | exactly one | — | API token source |
| `auto_allow_zt` | no | `true` | Add `config.routes[].target` with `via == nil` to allowlist |
| `allowed_networks` | no (repeatable) | empty | Extra allowed CIDRs |
| `strict_start` | no | `false` | If cache not initialized yet: `true` => REFUSED, `false` => allow |
| `search_domain` | no | `zone` | Used in DNS-SD TXT answer |
| `allow_short_names` | no | `false` | Resolve bare names as `<name>.<zone>` |
| `ttl` | no | `60` | DNS TTL |
| `refresh` | no | `30s` | Refresh interval |
| `timeout` | no | `5s` | HTTP timeout |
| `max_retries` | no | `3` | Retries for API 5xx / transport errors |

## DNS behavior

- Query without questions: `SERVFAIL`.
- Out-of-zone query: passed to next plugin.
- Zone query from disallowed source: `REFUSED`.
- Known name/no matching type: `NOERROR` with empty answer.
- Unknown name in zone: `NXDOMAIN` with SOA in authority.

Records are built for two names per authorized member:
1. `<member-name>.<zone>` (spaces replaced with `_`, lowercased)
2. `<nodeId>.<zone>`

## Metrics

Exposes Prometheus metrics:

- `coredns_ztnet_requests_total{zone,rcode}`
- `coredns_ztnet_refused_total{zone}`
- `coredns_ztnet_cache_refresh_total{zone,status}`
- `coredns_ztnet_cache_entries{zone,type}`
- `coredns_ztnet_token_reload_total{zone,source,status}`

## Build on Linux from source

### 1) Prepare environment

Install Go `1.24+` (patched release recommended, e.g. `1.25.7+`) and clone the repository:

```bash
git clone https://github.com/CleoWixom/ztnet-dns.git
cd ztnet-dns
```

For Linux systems with `apt`, install required build dependencies via Makefile:

```bash
make install-deps
```

If Go is not installed, run:

```bash
make ensure-go
```

### 2) Verify plugin source

```bash
make verify
```

Check that DNS port 53 is free before install/start:

```bash
make check-port
```

If you need to inspect existing listeners and ZT interfaces:

```bash
make verify-bind-scope
```

### 3) Build plugin packages (repository local)

```bash
make build-plugin
```

### 4) Build CoreDNS with ztnet plugin (external plugin flow)

> **Compatibility note:** build this plugin with the same CoreDNS branch/version used for the final binary. Mixing mismatched CoreDNS/quic-go versions can break QUIC build (e.g. `undefined: quic.Connection`).

Build using the automated target:

```bash
make build-coredns
```

This target clones CoreDNS `v1.14.0`, injects `ztnet` into `plugin.cfg`, runs `go generate`, and builds a Linux `amd64` CoreDNS binary in `$(COREDNS_WORKDIR)/coredns`.

Use `Corefile.example` from this repo as a starting point and configure the `ztnet` block for your environment.

### 5) Install helper script into system PATH

For manual installs, run a full flow:

```bash
sudo make install
```

`make install` performs: `ensure-go`, `check-port`, `verify`, `build-coredns`, binary install (`/usr/sbin/coredns`), config/unit install (`/etc/coredns`, `/lib/systemd/system`), helper install (`/usr/bin/ztnet.token.install`), service activation, and ZeroTier service compatibility patch (`/usr/sbin/zerotier-one` -> `/usr/sbin/zerotier-one -U`) when `/lib/systemd/system/zerotier-one.service` exists.

For package installs (`.deb`), the helper is installed automatically to `/usr/bin/ztnet.token.install`.


### 6) Settings (configuration)

To automate secure token setup/rotation, use:

```bash
sudo ztnet.token.install --help
```

The script automates:
- token input via argument, stdin (pipe), or hidden interactive input,
- secure save to `/run/secrets/ztnet_token`,
- permission hardening via `chown root:coredns` and `chmod 0440`,
- `token_file` verification in Corefile,
- secure token rotation without storing secrets in Corefile.

**About `zt.example.com { ... }` and `zone zt.example.com`:** the first value is the CoreDNS server block zone, while `zone` is an explicit plugin setting required by `ztnet` parser. Keep them identical to avoid confusing behavior and to make configuration intent explicit.

To expose DNS only on ZeroTier interfaces, configure CoreDNS `bind`/`listen` directives for your `zt*` interfaces in Corefile.

#### 6.1 Generate a ZTNET API token

Generate a token in the ZTNET UI (API tokens / personal token), then provide it to the script using one of the methods below.

#### 6.2 Basic token install (argument)

```bash
sudo ztnet.token.install "<ZTNET_API_TOKEN>"
```

#### 6.3 Token install via stdin

```bash
printf '%s\\n' "<ZTNET_API_TOKEN>" | sudo ztnet.token.install
```

#### 6.4 Interactive token input (hidden)

```bash
sudo ztnet.token.install
```

#### 6.5 Token rotation

```bash
sudo ztnet.token.install "<NEW_ZTNET_API_TOKEN>" --rotate
```

#### 6.6 Additional options

```bash
# if Corefile path or service group differs from defaults
sudo ztnet.token.install "<TOKEN>" \
  --token-file /run/secrets/ztnet_token \
  --corefile /etc/coredns/Corefile \
  --group coredns
```

By default, the script checks for this exact line:

```corefile
token_file /run/secrets/ztnet_token
```

If your Corefile uses a different token path, pass it explicitly via `--token-file`.


## One-command install for Ubuntu 24.04

Run installer directly from GitHub:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/CleoWixom/ztnet-dns/main/scripts/install-ubuntu-24.04.sh)
```

The script supports both regular user (with `sudo`) and direct `root` execution.

The installer will interactively request and validate:
- `TOKEN ZTNET API` file path (default: `/run/secrets/ztnet_token`); token is read from file and re-checked until valid
- `API_URL` (default: `http://127.0.0.1:3000`, validates format/reachability)
- `ZONE` (default: `zt.local`)
- `NETWORK_ID` (default: empty, with format validation: 16-hex network id)

If token validation fails, the script asks you to update the token file and repeats validation. After validation it installs CoreDNS + ztnet plugin, writes `/etc/coredns/Corefile`, securely saves token to `/run/secrets/ztnet_token`, and starts `coredns-ztnet.service`.

## Development checks

```bash
go test ./... -race -count=1
golangci-lint run ./...
```

## Security notes

- Avoid `api_token` inline in production; prefer `token_file` or `token_env`.
- Token is loaded during refresh and is not persisted in plugin state.
- Keep Go patch version updated (stdlib vulnerabilities are fixed via Go toolchain patch updates).

## Versioning and lifecycle targets

Plugin build version is embedded at compile time from git metadata (`make version`), and automated release tags are created by GitHub Actions.

### Automatic release workflow

Repository includes workflow `.github/workflows/release.yml` with behavior:

- Trigger: `push` to `main` **only** when changed files match `**/*.go`, `go.mod`, or `go.sum`.
- Before tagging: runs `go test ./...`.
- Tag format: `v1.2.3`.
- Default bump: `patch`.
- Bump control from commit message markers: `#major`, `#minor`, `#patch`.
- After tag creation: creates GitHub Release with auto-generated changelog.

### How to use

1. Make code changes in `.go` files (or `go.mod` / `go.sum`).
2. Commit with optional bump marker in commit message:
   - `#patch` (or no marker) → patch bump,
   - `#minor` → minor bump,
   - `#major` → major bump.
3. Push to `main`.
4. Workflow runs tests, creates next tag, and publishes GitHub Release automatically.

Examples:

```bash
git commit -m "fix: handle token rotation #patch"
git commit -m "feat: add dns-sd enhancements #minor"
git commit -m "refactor!: change API behavior #major"
```
