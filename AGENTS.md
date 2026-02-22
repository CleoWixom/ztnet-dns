# AGENTS.md — Development Guide for `ztnet-dns`

This file is the authoritative reference for AI coding agents (GitHub Copilot, Claude, Codex)
and human contributors working on this repository. Read it before making any changes.

---

## Repository purpose

`ztnet-dns` is a **CoreDNS external plugin** that serves A/AAAA DNS records for authorized
ZeroTier network members fetched from a ZTNET API. It is security-sensitive infrastructure:
it runs as a DNS server on a private network and must never leak topology data to external clients.

---

## File map

```
ztnet-dns/
├── setup.go      — Corefile parsing, plugin registration, lifecycle (caddy hooks)
├── ztnet.go      — ZtnetPlugin struct, ServeDNS handler, refresh loop, SOA record
├── access.go     — AllowedNets type, CIDR allowlist, extractSourceIP
├── cache.go      — RecordCache (atomic.Value + cacheSnapshot), LookupA/AAAA/IsAllowed
├── secret.go     — TokenConfig, LoadToken (file/env/inline sources)
├── api.go        — ZTNET HTTP client, Member/NetworkInfo structs, retry logic
├── ipv6.go       — ComputeRFC4193, Compute6plane (pure math, no I/O)
├── metrics.go    — Prometheus counters/gauges, registered via CoreDNS metrics package
└── ztnet_test.go — All tests: unit, integration (httptest), DNS handler tests
```

**One rule:** each file has exactly one responsibility. Do not bleed concerns across files.

---

## Critical invariants — never break these

### 1. ServeDNS execution order

```
len(r.Question) == 0  →  SERVFAIL          (guard)
qname not in zone     →  plugin.NextOrFailure  ← MUST be before source IP check
source IP not allowed →  REFUSED           ← only for ZT zone queries
cache lookup          →  answer / NXDOMAIN
```

**Why the order matters:** ZT clients use this DNS server for both internal names
(`server01.zt.example.com`) AND global internet (`google.com`). If source-IP check
runs before zone check, `google.com` queries from ZT clients get `REFUSED` and the
internet stops working for all ZT members. Zone check must come first.

### 2. Cache is the hot path — zero allocations

`ServeDNS` must not:
- Make any network calls
- Acquire any mutex locks (use `atomic.Value`)
- Allocate strings beyond the single `strings.ToLower(q.Name)`
- Call `dns.Msg.Copy()`

### 3. AllowedNets lives inside cacheSnapshot

```go
// CORRECT — atomic consistency guaranteed
type cacheSnapshot struct {
    a       map[string][]net.IP
    aaaa    map[string][]net.IP
    allowed *AllowedNets
}

// WRONG — race between records update and allowlist update
type ZtnetPlugin struct {
    cache   *RecordCache
    allowed *AllowedNets   // ← DO NOT do this
}
```

DNS records and the IP allowlist must be updated in a single `atomic.Store` so there is
never a moment where records are new but the allowlist is stale (or vice versa).

### 4. Token never stored in struct fields

```go
// CORRECT — token lives only on the stack of refresh()
func (p *ZtnetPlugin) refresh(ctx context.Context) error {
    token, err := LoadToken(p.cfg.Token)
    // use token, then it goes out of scope
}

// WRONG
type Config struct {
    Token string   // ← plaintext secret in heap, visible in heap dumps
}
```

### 5. Stale-on-error for both cache and allowlist

If ZTNET API returns an error during refresh:
- Keep the existing DNS records (stale cache)
- Keep the existing allowlist
- Log the error at WARNING level
- Continue the refresh timer — try again on next tick
- Never return an empty allowlist (that would let everyone in or lock everyone out)

### 6. REFUSED is for ZT zone queries from outside, not for global DNS

The plugin only access-controls its own zone. It does NOT restrict:
- `google.com` queries from anyone (those go to `forward`)
- Any query that doesn't match the plugin's zone

---

## Code conventions

### Imports

Always import CoreDNS logger, never stdlib `log` or `fmt.Println`:

```go
import (
    "github.com/coredns/coredns/plugin"
    clog "github.com/coredns/coredns/plugin/log"
)

// Usage:
clog.Infof("ztnet: zone %s refreshed", zone)
clog.Warningf("ztnet: REFUSED from %s", srcIP)
clog.Errorf("ztnet: API error: %v", err)
clog.Debugf("ztnet: cache has %d A entries", n)
```

### Error wrapping

Always wrap errors with context:

```go
// CORRECT
return fmt.Errorf("fetch members: %w", err)
return fmt.Errorf("token_file read: %w", err)

// WRONG
return err
```

### DNS name normalization

Do it exactly once per `ServeDNS` call, at the top:

```go
qname := strings.ToLower(q.Name)
// use qname everywhere below, never q.Name directly
```

DNS names in cache keys always end with a trailing dot and are lowercase:
`"server01.zt.example.com."` — never `"Server01.zt.example.com"`.

### Returning from ServeDNS

```go
// For answers:
return dns.RcodeSuccess, nil

// For NXDOMAIN (always include SOA in Authority section):
m.Ns = append(m.Ns, p.soaRecord())
return dns.RcodeNameError, nil

// For refused:
return dns.RcodeRefused, nil

// For passing to next plugin:
return plugin.NextOrFailure(p.Name(), p.Next, ctx, w, r)

// NEVER return an error unless it's a genuine internal failure
// (not NXDOMAIN, not REFUSED — those are valid DNS responses, not Go errors)
```

### No `os.Exit` anywhere

```go
// WRONG
if err != nil { os.Exit(1) }

// CORRECT
if err != nil { return plugin.Error("ztnet", err) }
```

---

## Testing requirements

Every change must maintain or improve test coverage. Tests live in `ztnet_test.go`.

### Test categories

**Unit — cache:**
- `TestCacheSnapshot` — Set + Lookup, verify old snapshot is not mutated
- `TestCacheConcurrency` — 50 readers, 5 writers, `go test -race` must pass
- `TestCacheIsAllowed` — verify AllowedNets flows through snapshot correctly

**Unit — access control:**
- `TestAllowedNets_*` — Contains for v4/v6/loopback/external, invalid CIDR error
- `TestExtractSourceIP_*` — UDP, TCP, fallback string parse
- `TestServeDNS_REFUSED_*` — external IP, nil IP, loopback passthrough

**Unit — token:**
- `TestLoadToken_*` — file (trim whitespace), env, inline, empty file, missing file, hot-rotation

**Unit — IPv6:**
- `TestComputeRFC4193` — reference vector: `efcc1b0947` / `17d395d8cb43a800` → `fd17:d395:...`
- `TestCompute6plane` — reference vector → `fc1c:903d:...`

**Unit — ServeDNS flow:**
- `TestServeDNS_A`, `_AAAA`, `_ANY`, `_SOA`, `_NXDOMAIN`, `_OutOfZone`, `_NoQuestion`
- `TestServeDNS_GlobalDNS_Passthrough` — query for `google.com` passes to Next, NOT refused
- `TestServeDNS_DNSSD_TXT`
- `TestServeDNS_ShortName_*`

**Integration — API (httptest.NewServer):**
- `TestFetchMembers_Success`, `_OnlyAuthorized`, `_401`, `_500ThenOK`, `_Timeout`
- `TestRefresh_TokenRotation` — verify second refresh sends new token in header
- `TestRefresh_StaleOnAPIError` — API fails, DNS still works with old data
- `TestRefresh_AutoCIDRFromRoutes` — `via==nil` routes → in allowlist
- `TestRefresh_SkipsViaRoutes` — `via!=nil` routes → NOT in allowlist

### Running tests

```bash
# All tests with race detector (required before any PR)
go test ./... -race -count=1

# Specific test
go test -run TestServeDNS_GlobalDNS_Passthrough -v

# With coverage
go test ./... -race -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Test helpers

Use CoreDNS test utilities for DNS message construction:

```go
import "github.com/coredns/coredns/plugin/test"

// Build a fake request
req := new(dns.Msg)
req.SetQuestion("server01.zt.example.com.", dns.TypeA)

// Use test.ResponseWriter to capture the response
rw := test.NewResponseWriter()
rcode, err := plugin.ServeDNS(ctx, rw, req)

// Verify with test.Case
tc := test.Case{
    Qname: "server01.zt.example.com.", Qtype: dns.TypeA,
    Answer: []dns.RR{test.A("server01.zt.example.com. 60 IN A 10.147.20.5")},
}
if err := tc.Validate(rw.Msg); err != nil {
    t.Errorf("unexpected response: %v", err)
}
```

For access control tests, implement a fake `dns.ResponseWriter` with a configurable `RemoteAddr`:

```go
type fakeRW struct {
    test.ResponseWriter
    remoteAddr net.Addr
}

func (f *fakeRW) RemoteAddr() net.Addr { return f.remoteAddr }

// Usage:
rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 1234}}
rcode, _ := p.ServeDNS(ctx, rw, req)
assert(rcode == dns.RcodeRefused)
```

---

## Adding a feature — checklist

- [ ] Update `setup.go` parser for any new Corefile parameters
- [ ] Update parameter validation (missing required field → `plugin.Error`)
- [ ] Update `Config` struct (keep `TokenConfig` separate, not raw string)
- [ ] If the feature touches `cacheSnapshot` — update both `Set()` and all consumers
- [ ] Add tests covering the happy path, error path, and concurrent access if applicable
- [ ] Update `README.md` parameter table
- [ ] Run `go test ./... -race` — must pass clean
- [ ] Run `golangci-lint run` — zero warnings

---

## What NOT to do

| Don't | Do instead |
|-------|-----------|
| `log.Printf(...)` | `clog.Infof(...)` |
| `os.Exit(1)` | `return plugin.Error("ztnet", err)` |
| Store token in `Config` struct | Use `TokenConfig{Source, Value}`, resolve in `refresh()` |
| Put `AllowedNets` in `ZtnetPlugin` | Put it in `cacheSnapshot` |
| Check source IP before zone check | Check zone first → forward global DNS → then check source |
| Return `NXDOMAIN` for out-of-ZT requests | Return `REFUSED` — doesn't reveal zone existence |
| Use `sync.RWMutex` for cache | Use `atomic.Value` + immutable snapshot |
| Network calls in `ServeDNS` | Only cache reads in `ServeDNS`, network only in `refresh()` |
| `make(map...)` per DNS request | Build maps only in `refresh()`, share immutable snapshot |
| Retry on 401/403 | Return `ErrUnauthorized` immediately, log ERROR |
| Log the token value | Never log secrets — not even the first 4 chars |
| `c.OnShutdown` | `c.OnFinalShutdown` |

---

## Dependency policy

- **No new direct dependencies** without discussion
- `net/http`, `encoding/json`, `sync/atomic`, `context` — use stdlib
- `github.com/miekg/dns` — already pulled by CoreDNS, use it
- `github.com/coredns/coredns` — use its `plugin`, `log`, `metrics` packages
- Prometheus — use `github.com/coredns/coredns/plugin/metrics`, not raw `prometheus/client_golang`
- No HTTP client libraries (resty, go-resty, etc.) — stdlib `net/http` is sufficient

---

## Security rules for agents

These rules are non-negotiable. Any PR that violates them will be rejected.

1. **Scope of REFUSED:** `REFUSED` applies only to ZT-zone queries from non-ZT sources.
   Global DNS forwarding must never be blocked.

2. **Token hygiene:** Token values must never appear in: log output, error messages,
   struct fields (beyond the initial `TokenConfig.Value` for inline source), or HTTP headers
   that get logged.

3. **Allowlist atomicity:** `AllowedNets` must be updated atomically with DNS records
   inside `cacheSnapshot`. Separate updates create a TOCTOU window.

4. **Stale-on-error:** An API error must never result in an empty allowlist or empty
   DNS cache. Always preserve the previous state.

5. **Loopback hardcoded:** `127.0.0.0/8` and `::1/128` are always in the allowlist.
   No configuration option can remove them.

6. **No open resolver for ZT zone:** The ZT zone must never be served to external clients,
   regardless of query type (A, AAAA, ANY, TXT, SOA, etc.).

---

## Glossary

| Term | Meaning |
|------|---------|
| ZT | ZeroTier — the VPN/overlay network technology |
| ZTNET | The web UI and API for managing ZeroTier networks |
| ZT member | A device authorized and connected to a ZeroTier network |
| ZT zone | The DNS zone served by this plugin (e.g. `zt.example.com`) |
| Snapshot | An immutable `cacheSnapshot` stored in `atomic.Value` |
| Allowlist | `AllowedNets` — CIDR list of IPs allowed to query the ZT zone |
| Stale serving | Serving the previous cache contents when the API is unreachable |
| Hot-rotation | Updating the API token file without restarting CoreDNS |
| RFC4193 | IPv6 address scheme used by ZeroTier for private network addressing |
| 6plane | Alternative ZeroTier IPv6 addressing scheme |
| REFUSED | DNS rcode 5 — server refuses to answer this client (RFC 1035) |
