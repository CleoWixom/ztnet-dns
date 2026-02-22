# Audit Report — ztnet-dns

**Дата:** 2026-02-22  
**Ревизия:** 4d52803  
**Аудитор:** Codex

## Сводка

| Уровень | Кол-во |
|---------|--------|
| [CRIT]  | 1      |
| [HIGH]  | 2      |
| [MED]   | 4      |
| [LOW]   | 1      |

## АУДИТ 0: Предварительная проверка

- `go test -race -count=1 -timeout=120s ./...` → PASS.
- `golangci-lint run --output.text.path stdout ./...` → PASS (совместимо с golangci-lint v2.x).
- `golangci-lint run ./...` → `0 issues`.

> Примечание по совместимости: флаг `--out-format` удалён в golangci-lint v2.x. Для v2 используйте `--output.*` флаги (например, `--output.text.path stdout`) или базовый `golangci-lint run ./...`.
- `git log --all --oneline -S "api_token" -- '*.go' '*.md' Corefile*` → есть исторические коммиты с `api_token`.
- `govulncheck ./...` (после установки) → обнаружены уязвимости в stdlib и зависимостях (в т.ч. CoreDNS, quic-go).

### [HIGH] Уязвимые версии в рантайме/зависимостях

**Файл:** `go.mod`, строки 7, 29
**Проблема:** фиксируется `github.com/coredns/coredns v1.11.4` и транзитивно `github.com/quic-go/quic-go v0.48.1`; `govulncheck` показывает multiple CVE/GO advisories для этих версий.
**Требование /AGENTS.md:** security-sensitive инфраструктура, нельзя игнорировать security risk.

**Текущий код:**
```go
require (
    github.com/coredns/coredns v1.11.4
)
...
    github.com/quic-go/quic-go v0.48.1 // indirect
```

**Исправление:**
```go
require github.com/coredns/coredns v1.14.0
// затем go mod tidy, повторный govulncheck
```bash
go test -race -count=1 -timeout=120s ./...
golangci-lint run --out-format=line-number ./...
git log --all --oneline -S "api_token" -- '*.go' '*.md' Corefile*
govulncheck ./...
```

- `go test -race -count=1 -timeout=120s ./...` → **PASS**.
- `golangci-lint run --out-format=line-number ./...` → **FAIL (unknown flag)** в текущей версии CLI.
- `golangci-lint run ./...` → **0 issues**.
- `git log -S "api_token" ...` → в истории есть изменения с inline token параметром (ожидаемо из эволюции плагина).
- `govulncheck ./...` → **не выполнен**, утилита отсутствует в окружении (`command not found`).

---

## Соответствие /AGENTS.md

| Требование | Статус | Комментарий |
|---|---|---|
| ServeDNS: no-question → out-of-zone passthrough → source ACL → cache | ✓ | Реализовано в `ztnet.go`. |
| AllowedNets внутри `cacheSnapshot` | ✓ | Реализовано в `cache.go`. |
| Stale-on-error для records + allowlist | ✓ | При ошибках refresh отсутствует `Set`, остаётся stale snapshot. |
| REFUSED только для запросов ZT-зоны | ✓ | Out-of-zone запросы уходят в `NextOrFailure`. |
| `c.OnFinalShutdown` | ✓ | Используется корректно. |
| Token загружается в `refresh()` (hot-rotation) | ✓ | `LoadToken` вызывается на каждом refresh. |
| Логгер CoreDNS: `plugin/log` | ✗ | Сейчас используется `plugin/pkg/log`. |
| Метрики через CoreDNS metrics wrapper | ✗ | Сейчас прямой `prometheus.MustRegister`. |
| Нет новых direct deps без необходимости | ✗ | Прямой `prometheus/client_golang` в `go.mod`. |
| Полный набор обязательных тестов из AGENTS | ✗ | Существенный недобор по security/correctness тестам. |

---

## Находки

### [CRIT] Некорректная реализация IPv6-алгоритмов RFC4193/6plane

**Файл:** `ipv6.go`  
**Проблема:** результат функций не совпадает с эталонными векторами из AGENTS.md. Это приводит к неправильным AAAA-адресам, рассинхронизации с ZeroTier и некорректному DNS-ответу.  
**Требование /AGENTS.md:** обязательные векторы:
- RFC4193: `fd17:d395:d8cb:43a8:1899:93ef:cc1b:0947`
- 6plane: `fc1c:903d:c0ef:cc1b:0947:0000:0000:0001`

**Текущий код:**
```go
h := sha256.Sum256(append(nb, node...))
ip[0] = 0xfd
copy(ip[1:6], h[:5])
copy(ip[6:16], node)
```

```go
ip[0] = 0xfc
ip[1] = nb[0]
ip[2] = nb[1]
ip[3] = nb[2]
ip[4] = nb[3]
copy(ip[5:10], node[:5])
```

**Наблюдение (фактический вывод):**
- RFC4193: `fd96:3fe2:4d62:efcc:1b09:4700::`
- 6plane: `fc17:d395:d8ef:cc1b:947::`

**Исправление:**
```go
// Привести алгоритмы в полное соответствие ZeroTier RFC4193/6plane спецификации
// и зафиксировать эталонными тест-векторами из AGENTS.md.
// После правки добавить/обновить:
// - TestComputeRFC4193
// - TestCompute6plane
```

### [HIGH] Нарушение политики регистрации метрик CoreDNS

**Файл:** `metrics.go`  
**Проблема:** метрики регистрируются напрямую через `prometheus.MustRegister`, а по требованиям нужно использовать CoreDNS wrapper (`plugin/metrics`). В смешанной среде CoreDNS это повышает риск конфликтов регистрации/инициализации.  
**Требование /AGENTS.md:** использовать `github.com/coredns/coredns/plugin/metrics`.

**Текущий код:**
```go
import "github.com/prometheus/client_golang/prometheus"
...
prometheus.MustRegister(requestCount)
```

**Исправление:**
```go
import (
    "github.com/coredns/coredns/plugin/metrics"
    "github.com/prometheus/client_golang/prometheus"
)

func init() {
    metrics.MustRegister(requestCount, refusedCount, refreshCount, entriesGauge, tokenReload)
}
```

### [HIGH] Отсутствуют обязательные тесты для security/correctness путей

**Файл:** `ztnet_test.go`  
**Проблема:** отсутствует значимая часть тестов из AGENTS.md (401 no-retry, timeout, token edge-cases, route filtering, short-name режимы, SOA/ANY, strict_start off, atomic allowed consistency и др.). Это снижает шанс поймать регрессии в критичном DNS path.

**Исправление:**
```go
// Добавить недостающие тесты из чеклиста AGENTS.md.
// Приоритет: access/security + api retry + ipv6 vectors + ServeDNS edge-cases.
```

### [MED] Используется `plugin/pkg/log` вместо `plugin/log`

**Файл:** `setup.go`, `ztnet.go`  
**Проблема:** импорт не соответствует установленной конвенции репозитория.  
**Требование /AGENTS.md:** `clog "github.com/coredns/coredns/plugin/log"`.

**Текущий код:**
```go
clog "github.com/coredns/coredns/plugin/pkg/log"
```

**Исправление:**
```go
clog "github.com/coredns/coredns/plugin/log"
```

### [MED] Прямой dependency на `prometheus/client_golang`

**Файл:** `go.mod`  
**Проблема:** dependency policy запрещает новые прямые зависимости, которые должны приходить транзитивно через CoreDNS.  
**Требование /AGENTS.md:** использовать CoreDNS `plugin/metrics`, не raw prometheus dependency как policy choice.

**Текущий код:**
```go
github.com/prometheus/client_golang v1.20.5
```

**Исправление:**
```bash
# после миграции metrics.go на CoreDNS metrics wrapper
go mod tidy
```

### [MED] Нет верификации эталонных IPv6-векторов в тестах

**Файл:** `ztnet_test.go`  
**Проблема:** есть только тест на invalid lengths, но нет тестов на expected addresses; из-за этого критический дефект в `ipv6.go` прошёл незамеченным.

**Исправление:**
```go
func TestComputeRFC4193(t *testing.T) { ... } // exact vector
func TestCompute6plane(t *testing.T) { ... }  // exact vector
```

### [MED] Не выполнен mandatory security-check `govulncheck`

**Файл:** N/A (окружение)  
**Проблема:** утилита отсутствует, поэтому обязательная часть аудита по CVE не завершена.

**Исправление:**
```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

### [LOW] Команда линтера из задания несовместима с локальной версией CLI

**Файл:** N/A (tooling)  
**Проблема:** `--out-format=line-number` не поддерживается установленным `golangci-lint`.

**Исправление:**
```bash
golangci-lint run ./...
# либо обновить golangci-lint до версии с поддержкой --out-format
```

---

## Проверка тестов (чеклист AGENTS.md)

### Реализовано
- `TestCacheSnapshot`
- `TestCacheConcurrency`
- `TestAllowedNets_Contains` (частично покрывает IPv4/loopback/external/mapped IPv6)
- `TestExtractSourceIP_UDP`
- `TestExtractSourceIP_TCP`
- `TestServeDNS_A`
- `TestServeDNS_AAAA`
- `TestServeDNS_NXDOMAIN`
- `TestServeDNS_UnknownType_NODATA` (частичный эквивалент)
- `TestServeDNS_GlobalDNS_Passthrough`
- `TestServeDNS_REFUSED_External`
- `TestServeDNS_NilAllowlist_StrictStart` (только strict=true)
- `TestLoadToken_FileEnvInline` (агрегированный)
- `TestLoadToken_HotRotation`
- `TestComputeInvalidLengths`
- `TestFetchMembers_Success`
- `TestRefresh_StaleOnAPIError`

### Отсутствуют (высокий приоритет)
- `TestFetchMembers_401` (ErrUnauthorized + no retry)
- `TestFetchMembers_500ThenOK`
- `TestFetchMembers_Timeout`
- `TestRefresh_TokenRotation`
- `TestRefresh_AutoCIDRFromRoutes`
- `TestRefresh_SkipsViaRoutes`
- `TestRefresh_StaleAllowedOnBuildError`
- `TestCache_SetIsAllowed_Atomic`
- `TestServeDNS_REFUSED_NilSrcIP`
- `TestServeDNS_NilAllowlist_StrictOff`
- `TestServeDNS_ANY`
- `TestServeDNS_SOA`
- `TestServeDNS_OutOfZone`
- `TestServeDNS_NoQuestion`
- `TestServeDNS_DNSSD_TXT`
- `TestServeDNS_DNSSD_Custom`
- `TestServeDNS_ShortName_Hit`
- `TestServeDNS_ShortName_Miss`
- `TestServeDNS_ShortName_Off`
- `TestIsBareName`
- `TestComputeRFC4193`
- `TestCompute6plane`

### Отсутствуют (средний приоритет)
- Раздельные тесты ошибок token source:
  - `TestLoadToken_File_Empty`
  - `TestLoadToken_File_Missing`
  - `TestLoadToken_Env_Unset`
  - `TestLoadToken_Inline` (отдельный кейс)
- Дополнительные granularity-тесты `AllowedNets` (IPv6 отдельным кейсом, invalid CIDR отдельным кейсом)

---

## АУДИТ по файлам

- `access.go` — критичных отклонений не выявлено; loopback hardcoded, IPv4-mapped IPv6 учитывается, `extractSourceIP` nil-safe.
- `cache.go` — atomic snapshot pattern корректный, `allowed` хранится внутри snapshot, strict-start логика поддержана.
- `secret.go` — token loading корректен (file/env/inline + empty checks), hot-rotation поддерживается.
- `api.go` — retry/unauthorized/timeout path в коде выглядит корректно, но недопокрыт тестами.
- `setup.go` — parsing корректен (включая ровно один token source, `OnFinalShutdown`, `search_domain` default), но logger import не по guideline.
- `ztnet.go` — порядок serve path соответствует AGENTS.md (zone check до ACL); stale-on-error сохраняется.
- `ipv6.go` — обнаружена критическая логическая ошибка алгоритмов (см. [CRIT]).
- `metrics.go` — нарушение policy по регистрации метрик.
- `ztnet_test.go` — тестов недостаточно для заявленного security-sensitive профиля.

---

## Результат после аудита

```text
go test -race -count=1 -timeout=120s ./...   → PASS
golangci-lint run ./...                      → 0 issues
govulncheck ./...                            → not run (tool missing)
```
