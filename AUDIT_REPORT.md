# Audit report (production readiness)

**Date:** 2026-02-22  
**Repository:** `ztnet-dns`

## Повторный запуск проверок

- `go mod tidy` — PASS
- `go test ./... -race -count=1` — PASS
- `govulncheck ./...` — FAIL (найдены reachable уязвимости)

## Итоговый статус

Архитектурно плагин соответствует production-паттерну (zone-first в `ServeDNS`, atomically-consistent snapshot cache, stale-on-error refresh, token loading per refresh).  
Блокер для production-эксплуатации остаётся: уязвимости в текущем runtime/dependency стеке.

## Критичные findings

`govulncheck` показывает reachable уязвимости из:

1. **Go stdlib/toolchain** (`crypto/tls`, `crypto/x509`, `net/http` и др.) для `go1.25.1`.
2. **`github.com/coredns/coredns@v1.11.3`** (в т.ч. GO-2026-4289).
3. **`github.com/quic-go/quic-go@v0.42.0`** (GO-2024-3302, linux).

## Что нужно сделать до продакшна

1. Обновить Go toolchain до патч-версии, закрывающей найденные stdlib advisories.
2. Обновить CoreDNS dependency минимум до `github.com/coredns/coredns v1.14.0` и прогнать совместимость.
3. После обновлений повторить:
   - `go mod tidy`
   - `go test ./... -race -count=1`
   - `govulncheck ./...`

## Вывод

- Функциональные тесты: **OK**.
- Security readiness для production: **NOT OK**, пока dependency/toolchain remediation не выполнен.
