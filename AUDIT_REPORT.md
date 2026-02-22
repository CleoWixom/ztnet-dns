# Security Audit Report

- **Date:** 2026-02-22
- **Tool install command:** `go install golang.org/x/vuln/cmd/govulncheck@latest`
- **Scan command:** `govulncheck ./...`
- **Result:** ❌ Vulnerabilities found (15 reachable vulnerabilities in project code).

## Found advisories

| Advisory | Affected module/package | Found in | Fixed in |
|---|---|---|---|
| GO-2026-4340 | stdlib (`crypto/tls`) | `crypto/tls@go1.25.1` | `crypto/tls@go1.25.6` |
| GO-2026-4337 | stdlib (`crypto/tls`) | `crypto/tls@go1.25.1` | `crypto/tls@go1.25.7` |
| GO-2026-4289 | `github.com/coredns/coredns` | `github.com/coredns/coredns@v1.11.4` | `github.com/coredns/coredns@v1.14.0` |
| GO-2025-4175 | stdlib (`crypto/x509`) | `crypto/x509@go1.25.1` | `crypto/x509@go1.25.5` |
| GO-2025-4155 | stdlib (`crypto/x509`) | `crypto/x509@go1.25.1` | `crypto/x509@go1.25.5` |
| GO-2025-4013 | stdlib (`crypto/x509`) | `crypto/x509@go1.25.1` | `crypto/x509@go1.25.2` |
| GO-2025-4012 | stdlib (`net/http`) | `net/http@go1.25.1` | `net/http@go1.25.2` |
| GO-2025-4011 | stdlib (`encoding/asn1`) | `encoding/asn1@go1.25.1` | `encoding/asn1@go1.25.2` |
| GO-2025-4010 | stdlib (`net/url`) | `net/url@go1.25.1` | `net/url@go1.25.2` |
| GO-2025-4009 | stdlib (`encoding/pem`) | `encoding/pem@go1.25.1` | `encoding/pem@go1.25.2` |
| GO-2025-4008 | stdlib (`crypto/tls`) | `crypto/tls@go1.25.1` | `crypto/tls@go1.25.2` |
| GO-2025-4007 | stdlib (`crypto/x509`) | `crypto/x509@go1.25.1` | `crypto/x509@go1.25.3` |
| GO-2025-3942 | `github.com/coredns/coredns` | `github.com/coredns/coredns@v1.11.4` | `github.com/coredns/coredns@v1.12.4` |
| GO-2025-3743 | `github.com/coredns/coredns` | `github.com/coredns/coredns@v1.11.4` | `github.com/coredns/coredns@v1.12.2` |
| GO-2024-3302 | `github.com/quic-go/quic-go` | `github.com/quic-go/quic-go@v0.48.1` | `github.com/quic-go/quic-go@v0.48.2` |

## Tasks (separate remediation items)

- [ ] **TASK-SEC-001 (stdlib / Go toolchain):** Upgrade Go runtime from `go1.25.1` to at least `go1.25.7` to cover reachable stdlib advisories:
  `GO-2026-4340`, `GO-2026-4337`, `GO-2025-4175`, `GO-2025-4155`, `GO-2025-4013`, `GO-2025-4012`, `GO-2025-4011`, `GO-2025-4010`, `GO-2025-4009`, `GO-2025-4008`, `GO-2025-4007`.
- [ ] **TASK-SEC-002 (module `github.com/coredns/coredns`):** Bump dependency from `v1.11.4` to `v1.14.0` (minimum safe version covering `GO-2026-4289`, and transitively covering `GO-2025-3942`, `GO-2025-3743`).
- [ ] **TASK-SEC-003 (module `github.com/quic-go/quic-go`):** Ensure resolved version is at least `v0.48.2` to remediate `GO-2024-3302`.

## Notes

- `govulncheck` exited with code `3`, indicating vulnerabilities were detected.
- Scan summary reported: 15 reachable vulnerabilities in code paths, plus additional non-reachable advisories in imports/modules.
