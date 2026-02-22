# ztnet-dns

CoreDNS external plugin for serving A/AAAA records of **authorized ZeroTier members** from ZTNET API.

## What the plugin does

- Serves DNS only for configured zone (`zone`).
- For queries outside configured zone, forwards request to next CoreDNS plugin (does not block global DNS).
- Applies source-IP access control only for plugin zone using allowlist from:
  - static `allowed_networks`
  - optional ZeroTier routes (`auto_allow_zt true`, only routes with `via == nil`)
  - always includes loopback ranges `127.0.0.0/8` and `::1/128`
- Periodically refreshes records and allowlist from API; on refresh errors keeps last good snapshot (stale-on-error).
- Supports token sources: `token_file`, `token_env`, `api_token` (exactly one).
- Supports short names (`allow_short_names`) and DNS-SD TXT (`_dns-sd._udp.<zone>` with `path=<search_domain>`).

## Requirements

- Go `1.22+` for build/tests.
- CoreDNS with plugin compiled in (see `plugin.cfg` workflow for external plugins).
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

## Behavior details

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

## Development checks

```bash
go test ./... -race -count=1
golangci-lint run ./...
```

## Security notes

- Avoid `api_token` inline in production; prefer `token_file` or `token_env`.
- Token is loaded during refresh and is not persisted in plugin state.
- Keep upstream dependencies and Go toolchain updated (see `AUDIT_REPORT.md`).
