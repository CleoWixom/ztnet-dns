# Audit report (production readiness)

**Date:** 2026-02-22  
**Repository:** `ztnet-dns`

## Summary

Current implementation is generally production-oriented by architecture (atomic cache snapshot, stale-on-error refresh, zone-first flow in `ServeDNS`, token loading per refresh).  
Main blocker for production hardening: **known reachable vulnerabilities in current dependency/runtime stack**.

## Checks performed

- `go test ./... -race -count=1` — PASS
- `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...` — FAIL (vulnerabilities detected)

## Findings

### 1) Dependency/runtime vulnerabilities (HIGH)

`govulncheck` reports reachable vulnerabilities for current environment and module graph:

- stdlib (`crypto/tls`, `crypto/x509`, `net/http`, etc.) tied to Go toolchain version in environment
- `github.com/coredns/coredns@v1.11.3`
- `github.com/quic-go/quic-go@v0.42.0` (indirect)

**Impact:** potential DoS / TLS / protocol-level risk in production DNS infrastructure.

**Recommended remediation:**
1. Upgrade Go toolchain to latest patched release available for your branch.
2. Upgrade `github.com/coredns/coredns` to at least `v1.14.0` and re-validate compatibility.
3. Re-run:
   - `go mod tidy`
   - `go test ./... -race -count=1`
   - `govulncheck ./...`

## Documentation status

`README.md` has been updated to match the current implementation (actual options, defaults, behavior, metrics, and operational notes).

## Conclusion

- Functional behavior and test baseline: **OK**.
- Security baseline for production: **NOT OK until dependency/toolchain updates are applied and re-scanned**.
