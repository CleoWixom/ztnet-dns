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
zt.example.com {
    ztnet {
        api_url    http://ztnet.local:3000
        network_id 8056c2e21c000001
        zone       zt.example.com

        # exactly one token source is required
        token_file /run/secrets/ztnet_token
        # token_env  ZTNET_API_TOKEN
        # api_token  dev-only-inline-token

        auto_allow_zt    true
        allowed_networks 10.147.0.0/16

        strict_start      false
        search_domain     zt.example.com
        allow_short_names false

        ttl         60
        refresh     30s
        timeout     5s
        max_retries 3
    }

    prometheus :9153
    log
    errors
}

. {
    forward . 1.1.1.1 8.8.8.8
    cache
    log
    errors
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

```bash
sudo apt-get update
sudo apt-get install -y git build-essential ca-certificates
```

Install Go `1.24+` and ensure `go version` reports a patched release (recommended: `1.25.7+`).

### 2) Clone and verify

```bash
git clone https://github.com/CleoWixom/ztnet-dns.git
cd ztnet-dns
go mod tidy
go test ./... -race -count=1
```

### 3) Build plugin package artifacts (repository local)

```bash
go build ./...
```

### 4) Integrate into a CoreDNS binary (external plugin flow)

> **Compatibility note:** build this plugin with the same CoreDNS branch/version used for the final binary. Mixing mismatched CoreDNS/quic-go versions can break QUIC build (e.g. `undefined: quic.Connection`).

1. In your CoreDNS source tree, add `ztnet:github.com/CleoWixom/ztnet-dns` to `plugin.cfg`.
2. Run CoreDNS build:

```bash
go generate
go build
```

3. Use `Corefile.example` from this repo as a starting point and configure `ztnet` block for your environment.


### 5) Settings (настройки / конфигурация)

Ниже рекомендуемый production-процесс для API токена и секрета.

#### 5.1 Генерация токена ZTNET API

1. Войдите в ZTNET под администратором.
2. Откройте раздел API tokens (или персональные токены доступа).
3. Создайте отдельный token только для CoreDNS/ztnet-dns.
4. По возможности выдавайте минимально необходимые права (read-only доступ к членам сети и конфигурации сети).
5. Скопируйте токен в безопасный буфер — повторно он может не отображаться.

#### 5.2 Сохранение токена в `/run/secrets/ztnet_token`

```bash
sudo install -d -m 0750 /run/secrets
sudo sh -c 'printf "%s\n" "<ZTNET_API_TOKEN>" > /run/secrets/ztnet_token'
```

#### 5.3 Права на файл секрета (обязательно)

```bash
# владелец root, группа сервиса DNS (пример: coredns)
sudo chown root:coredns /run/secrets/ztnet_token

# чтение только root и группе сервиса
sudo chmod 0440 /run/secrets/ztnet_token
```

Если сервис запускается от отдельного пользователя, укажите его группу вместо `coredns`.

#### 5.4 Проверка Corefile конфигурации

Убедитесь, что в блоке `ztnet` указан именно `token_file`:

```corefile
token_file /run/secrets/ztnet_token
```

И что **не** используются одновременно `token_env`/`api_token` (должен быть ровно один источник токена).

#### 5.5 Ротация токена без хранения в конфиге

При ротации токена:

```bash
sudo sh -c 'printf "%s\n" "<NEW_ZTNET_API_TOKEN>" > /run/secrets/ztnet_token'
sudo chown root:coredns /run/secrets/ztnet_token
sudo chmod 0440 /run/secrets/ztnet_token
```

Плагин перечитает токен на следующем refresh-цикле, без необходимости хранить токен в Corefile.

## Development checks

```bash
go test ./... -race -count=1
golangci-lint run ./...
```

## Security notes

- Avoid `api_token` inline in production; prefer `token_file` or `token_env`.
- Token is loaded during refresh and is not persisted in plugin state.
- Keep Go patch version updated (stdlib vulnerabilities are fixed via Go toolchain patch updates).
