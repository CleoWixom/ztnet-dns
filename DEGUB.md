# ztnet-dns Debug Guide (DEGUB)

This manual describes practical debugging and validation steps for `ztnet-dns` in development and production-like Linux environments.

## 1) Quick health checklist

1. CoreDNS starts without plugin init errors.
2. `ztnet` appears in plugin list.
3. In-zone requests from allowed sources return A/AAAA or NXDOMAIN (+ SOA).
4. In-zone requests from disallowed sources return REFUSED.
5. Out-of-zone requests are forwarded (not refused by `ztnet`).
6. Metrics endpoint exports `coredns_ztnet_*` counters/gauges.

## 2) Build & basic verification

From repository root:

```bash
make verify
make check-port
make build-plugin
```

To build CoreDNS with embedded `ztnet` plugin:

```bash
make build-coredns
```

For full Linux installation (build + binary/config/service/helper install):

```bash
sudo make install
```

To inspect port usage and zt* interfaces:

```bash
make check-port
make verify-bind-scope
```

The resulting binary is placed at:

```text
/tmp/coredns-ztnet-build/coredns
```

Check plugin registration:

```bash
/tmp/coredns-ztnet-build/coredns -plugins | grep ztnet
```

## 3) Minimal runtime config for debugging

Use a Corefile similar to:

```corefile
zt.example.com {
    ztnet {
        api_url    http://<ztnet-host>:3000
        network_id <network_id>
        zone       zt.example.com
        token_file /run/secrets/ztnet_token

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

Note on `zt.example.com { ... }` vs `zone zt.example.com`:
- `zt.example.com { ... }` is the CoreDNS server block zone routing scope.
- `zone zt.example.com` is a required `ztnet` plugin parameter.
- Keep both values identical; this is intentional, not a typo.

## 4) Token and secret debugging

Install/rotate token securely:

```bash
sudo ztnet.token.install --help
sudo ztnet.token.install "<ZTNET_API_TOKEN>"
```

Validate secret file properties:

```bash
ls -l /run/secrets/ztnet_token
# expected: -r--r----- root coredns
```

Validate Corefile token path line:

```bash
grep -n '^\s*token_file\s\+/run/secrets/ztnet_token\s*$' /etc/coredns/Corefile
```

## 5) Functional DNS checks

> Replace server IP and domain values with your environment.

### 5.1 In-zone A/AAAA lookup

```bash
dig @127.0.0.1 server01.zt.example.com A +short
dig @127.0.0.1 server01.zt.example.com AAAA +short
```

### 5.2 Unknown name in zone (must be NXDOMAIN + SOA)

```bash
dig @127.0.0.1 unknown-host.zt.example.com A
```

Expected:
- `status: NXDOMAIN`
- SOA present in AUTHORITY section

### 5.3 Out-of-zone forwarding (must not be refused by ztnet)

```bash
dig @127.0.0.1 google.com A
```

Expected: normal forwarded response (NOERROR), not REFUSED.

If you want DNS bound only to ZeroTier interfaces, enforce this via CoreDNS `bind`/`listen` directives on `zt*` interfaces.

### 5.4 REFUSED behavior for external/non-allowed source

From a source outside allowlist, query plugin zone:

```bash
dig @<dns_server_ip> server01.zt.example.com A
```

Expected: `status: REFUSED`.

## 6) Logs and metrics

### 6.1 Journal logs (systemd)

```bash
journalctl -u coredns-ztnet -f
```

Look for:
- refresh success/warnings
- API errors
- refused requests from non-allowed clients

### 6.2 Metrics check

```bash
curl -s http://127.0.0.1:9153/metrics | grep coredns_ztnet_
```

Useful metrics:
- `coredns_ztnet_requests_total{zone,rcode}`
- `coredns_ztnet_refused_total{zone}`
- `coredns_ztnet_cache_refresh_total{zone,status}`
- `coredns_ztnet_cache_entries{zone,type}`
- `coredns_ztnet_token_reload_total{zone,source,status}`

## 7) Typical failure scenarios

### 7.1 CoreDNS build fails with QUIC API errors

Symptoms like:
- `undefined: quic.Connection`

Actions:
1. Ensure CoreDNS version matches current plugin dependency line (`v1.14.0`).
2. Rebuild via `make build-coredns`.
3. Use patched Go toolchain version.

### 7.2 Requests to external domains are REFUSED

Root cause is usually wrong ServeDNS order in custom changes.

Expected order:
1. out-of-zone -> next plugin
2. source IP check only for in-zone queries

Run test:

```bash
go test -run TestServeDNS_GlobalDNS_Passthrough -v
```

### 7.3 Empty answers after API errors

Expected behavior is stale-on-error (old cache remains).

Run tests:

```bash
go test -run TestRefresh_StaleOnAPIError -v
```

### 7.4 Token issues

Symptoms:
- refresh unauthorized
- token reload failures

Actions:
1. rotate token file with `ztnet.token.install`
2. verify file perms/ownership
3. verify `token_file` path in Corefile

## 8) Test matrix for debugging changes

```bash
go test ./... -race -count=1
go test -run TestServeDNS_GlobalDNS_Passthrough -v
go test -run TestRefresh_StaleOnAPIError -v
go test -run TestRefresh_TokenRotation -v
```

## 9) Security validation checklist

- `token_file` is used (not inline `api_token`) in production.
- token file is `0440`, owner `root`, group service group.
- plugin zone is not exposed to external sources (REFUSED outside allowlist).
- out-of-zone traffic is forwarded (no accidental open blocking).
- stale cache/allowlist behavior is preserved during API outages.
