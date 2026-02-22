# Audit Report — ztnet-dns

**Дата:** 2026-02-22
**Ревизия:** 5c78546
**Аудитор:** Codex

## Сводка

| Уровень | Кол-во |
|---------|--------|
| [CRIT]  | 4      |
| [HIGH]  | 10     |
| [MED]   | 12     |
| [LOW]   | 4      |

## АУДИТ 0: Предварительная проверка

- `go test -race -count=1 -timeout=120s ./...` → PASS.
- `golangci-lint run --out-format=line-number ./...` → команда не поддерживается текущей версией CLI (unknown flag).
- `golangci-lint run ./...` → `0 issues`.
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
```

### [HIGH] Уязвимая версия toolchain в окружении сканирования

**Файл:** N/A (окружение Go)
**Проблема:** `govulncheck` нашёл уязвимости stdlib (`crypto/tls`, `crypto/x509`, `net/http`) на `go1.25.1`.
**Требование /AGENTS.md:** security-sensitive контур.

**Текущий код:**
```text
Found in: crypto/tls@go1.25.1
Found in: crypto/x509@go1.25.1
Found in: net/http@go1.25.1
```

**Исправление:**
```text
Обновить CI/production toolchain до Go patch release, содержащего фиксы (>=1.25.7 по выводу сканера).
```

## Соответствие /AGENTS.md

| Требование | Статус |
|---|---|
| Порядок ServeDNS: no-question → out-of-zone passthrough → source ACL | ✓ |
| AllowedNets внутри cacheSnapshot | ✓ |
| Stale-on-error для records и allowlist | ✓ (через early return без Set) |
| REFUSED только для ZT-zone | ✓ |
| c.OnFinalShutdown, не OnShutdown | ✓ |
| Ровно один источник токена | ✗ |
| search_domain по умолчанию = zone | ✗ |
| logger import через `plugin/log` | ✗ |
| metrics через CoreDNS metrics API | ✗ |
| Тест-покрытие согласно списку | ✗ |

## Находки

### [CRIT] `strict_start` фактически не работает: allowlist никогда не nil

**Файл:** `cache.go`, строка 20; `ztnet.go`, строки 51-53
**Проблема:** `NewRecordCache()` и `Set()` принудительно подставляют `&AllowedNets{}`; это делает невозможным состояние `snap.allowed == nil`, поэтому `strict_start=true` не может блокировать до первого refresh.
**Требование /AGENTS.md:** поведение strict_start должно различать nil allowlist на старте.

**Текущий код:**
```go
rc.snap.Store(cacheSnapshot{..., allowed: &AllowedNets{}})
...
if allowed == nil {
    allowed = &AllowedNets{}
}
```

**Исправление:**
```go
// cache snapshot может хранить allowed=nil до первого успешного refresh
rc.snap.Store(cacheSnapshot{a: map[string][]net.IP{}, aaaa: map[string][]net.IP{}, allowed: nil})

func (r *RecordCache) IsAllowed(ip net.IP, strictStart bool) bool {
    s := r.load()
    if s.allowed == nil {
        return !strictStart
    }
    return s.allowed.Contains(ip)
}
```

### [CRIT] Нет никакого логирования REFUSED (и нет WARNING с source IP)

**Файл:** `ztnet.go`, строки 51-60
**Проблема:** на REFUSED инкрементируются метрики и пишется ответ, но не логируется источник.
**Требование /AGENTS.md:** REFUSED должен логироваться на WARNING с source IP.

**Текущий код:**
```go
if !p.cache.IsAllowed(src) {
    ...
    return dns.RcodeRefused, nil
}
```

**Исправление:**
```go
clog.Warningf("ztnet: REFUSED query name=%s type=%d src=%v", qname, q.Qtype, src)
```

### [CRIT] Порядок ветвления ломает требование по UnknownType

**Файл:** `ztnet.go`, строки 65-96
**Проблема:** любой неподдерживаемый qtype сейчас становится NXDOMAIN (`len(m.Answer)==0`), хотя корректное поведение — NOERROR с пустым Answer.
**Требование /AGENTS.md:** в обязательных тестах есть `TestServeDNS_UnknownType` с ожидаемым NOERROR.

**Текущий код:**
```go
if len(m.Answer) == 0 {
    m.Rcode = dns.RcodeNameError
    ...
}
```

**Исправление:**
```go
foundName := len(p.cache.LookupA(qname)) > 0 || len(p.cache.LookupAAAA(qname)) > 0
if len(m.Answer) == 0 {
    if foundName {
        m.Rcode = dns.RcodeSuccess // NODATA
    } else {
        m.Rcode = dns.RcodeNameError
        m.Ns = append(m.Ns, p.soaRecord())
    }
}
```

### [CRIT] Несоблюдение политики метрик CoreDNS

**Файл:** `metrics.go`, строки 4, 16-20
**Проблема:** используется прямой `prometheus.MustRegister`, а не CoreDNS metrics wrapper.
**Требование /AGENTS.md:** регистрировать через `github.com/coredns/coredns/plugin/metrics`.

**Текущий код:**
```go
import "github.com/prometheus/client_golang/prometheus"
...
prometheus.MustRegister(...)
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

### [HIGH] Нет валидации «ровно один источник токена»

**Файл:** `setup.go`, строки 55-60, 96-98
**Проблема:** повторная директива просто перезаписывает `cfg.Token`; конфликт не детектируется.
**Требование /AGENTS.md:** ошибка если задано более одного источника.

**Текущий код:**
```go
case "token_file": cfg.Token = ...
case "token_env": cfg.Token = ...
case "api_token": cfg.Token = ...
```

**Исправление:**
```go
var tokenSources int
// в каждой ветке token_*: tokenSources++
if tokenSources != 1 { return cfg, fmt.Errorf("exactly one token source required") }
```

### [HIGH] HTTP клиент без таймаутов transport

**Файл:** `setup.go`, строка 25
**Проблема:** `HTTPClient: &http.Client{}` без timeout и transport constraints.
**Требование /AGENTS.md:** security/perf устойчивость, отсутствие зависания запросов.

**Текущий код:**
```go
HTTPClient: &http.Client{}
```

**Исправление:**
```go
tr := &http.Transport{
    DialContext: (&net.Dialer{Timeout: cfg.Timeout / 2}).DialContext,
    TLSHandshakeTimeout:   cfg.Timeout / 2,
    ResponseHeaderTimeout: cfg.Timeout,
    IdleConnTimeout:       90 * time.Second,
}
HTTPClient: &http.Client{Transport: tr, Timeout: cfg.Timeout}
```

### [HIGH] `defer resp.Body.Close()` внутри retry loop

**Файл:** `api.go`, строки 42-55
**Проблема:** defer копится до выхода из `getJSON`, при множественных retry держатся открытые body/conn дольше нужного.
**Требование /AGENTS.md:** корректное управление HTTP body.

**Текущий код:**
```go
for ... {
    resp, err := c.HTTPClient.Do(req)
    ...
    defer func(){ _ = resp.Body.Close() }()
```

**Исправление:**
```go
resp, err := c.HTTPClient.Do(req)
if err != nil { ... }
func() {
    defer resp.Body.Close()
    ... // read/decode
}()
```

### [HIGH] Ошибка создания request игнорируется

**Файл:** `api.go`, строка 43
**Проблема:** `req, _ := http.NewRequestWithContext(...)` теряет ошибку.
**Требование /AGENTS.md:** оборачивать ошибки контекстом.

**Текущий код:**
```go
req, _ := http.NewRequestWithContext(...)
```

**Исправление:**
```go
req, err := http.NewRequestWithContext(...)
if err != nil { return fmt.Errorf("build request %s: %w", path, err) }
```

### [HIGH] `search_domain` не нормализуется и не получает default=zone

**Файл:** `setup.go`, строка 83, отсутствие post-parse default
**Проблема:** `search_domain` сохраняется как есть (может быть без точки/в mixed-case); если не задан — остаётся пустой.
**Требование /AGENTS.md:** default должен быть `zone`.

**Текущий код:**
```go
case "search_domain":
    cfg.SearchDomain = args[0]
```

**Исправление:**
```go
if cfg.SearchDomain == "" { cfg.SearchDomain = cfg.Zone }
cfg.SearchDomain = dns.Fqdn(strings.ToLower(cfg.SearchDomain))
```

### [HIGH] Неправильный logger import

**Файл:** `setup.go`, строка 14; `ztnet.go`, строка 11
**Проблема:** используется `plugin/pkg/log` вместо `plugin/log`.
**Требование /AGENTS.md:** импортировать CoreDNS logger из `github.com/coredns/coredns/plugin/log`.

**Текущий код:**
```go
clog "github.com/coredns/coredns/plugin/pkg/log"
```

**Исправление:**
```go
clog "github.com/coredns/coredns/plugin/log"
```

### [HIGH] `Route` не соответствует именованию из требований

**Файл:** `api.go`, строки 22-25
**Проблема:** публичный тип называется `Route`, в требованиях фигурирует `NetworkRoute`; это ухудшает читаемость API и рассинхронизирует документацию.
**Требование /AGENTS.md:** glossary/чеклист упоминает `NetworkRoute`.

**Текущий код:**
```go
type Route struct { ... }
```

**Исправление:**
```go
type NetworkRoute struct { ... }
```

### [HIGH] DNS A-record строится из IP без `To4()`

**Файл:** `ztnet.go`, строки 68, 76
**Проблема:** `dns.A.A` получает `ip` как есть; из `net.ParseIP` это часто 16-byte representation.
**Требование /AGENTS.md:** корректный TypeA wire-format.

**Текущий код:**
```go
A: ip
```

**Исправление:**
```go
A: ip.To4()
```

### [MED] Нет параллельного fetch members/network в refresh

**Файл:** `ztnet.go`, строки 144-153
**Проблема:** вызовы последовательные, latency = сумма двух API calls.
**Требование /AGENTS.md:** рекомендация о производительности горячего пути/refresh эффективности.

**Текущий код:**
```go
members, err := p.api.FetchMembers(...)
netinfo, err := p.api.FetchNetwork(...)
```

**Исправление:**
```go
var members []Member
var netinfo NetworkInfo
var errM, errN error
var wg sync.WaitGroup
wg.Add(2)
go func(){ defer wg.Done(); members, errM = p.api.FetchMembers(ctx, token) }()
go func(){ defer wg.Done(); netinfo, errN = p.api.FetchNetwork(ctx, token) }()
wg.Wait()
```

### [MED] `allowed_networks` парсит только первый аргумент

**Файл:** `setup.go`, строка 66
**Проблема:** при нескольких CIDR в одной строке учитывается только `args[0]`.
**Требование /AGENTS.md:** корректный парсинг Corefile параметров.

**Текущий код:**
```go
cfg.AllowedCIDRs = append(cfg.AllowedCIDRs, args[0])
```

**Исправление:**
```go
cfg.AllowedCIDRs = append(cfg.AllowedCIDRs, args...)
```

### [MED] Игнорируются ошибки ParseBool/ParseDuration/Atoi

**Файл:** `setup.go`, строки 63, 68, 71, 74, 77, 80, 85
**Проблема:** invalid input silently falls back to zero values.
**Требование /AGENTS.md:** parse-time validation with `plugin.Error`.

**Текущий код:**
```go
v, _ := strconv.ParseBool(args[0])
```

**Исправление:**
```go
v, err := strconv.ParseBool(args[0])
if err != nil { return cfg, fmt.Errorf("strict_start parse: %w", err) }
```

### [MED] `Contains` не нормализует IPv4-mapped IPv6

**Файл:** `access.go`, строка 33
**Проблема:** адрес вида `::ffff:10.0.0.1` может не матчиться против IPv4 CIDR.
**Требование /AGENTS.md:** корректность ACL, включая edge-cases.

**Текущий код:**
```go
if n.Contains(ip) { ... }
```

**Исправление:**
```go
if v4 := ip.To4(); v4 != nil { ip = v4 }
```

### [MED] Отсутствует строгая проверка длины networkID/nodeID в IPv6

**Файл:** `ipv6.go`, строки 13-18, 31-36
**Проблема:** проверяется только `len>0` и `len>=4/5`; некорректные длины принимаются.
**Требование /AGENTS.md:** nodeID=10 hex, networkID=16 hex.

**Текущий код:**
```go
if err != nil || len(nb) == 0 { ... }
```

**Исправление:**
```go
if len(networkID) != 16 { return nil, fmt.Errorf("invalid networkID length") }
if len(nodeID) != 10 { return nil, fmt.Errorf("invalid nodeID length") }
```

### [MED] Нет проверки пустого `Route.Target` при AutoAllowZT

**Файл:** `ztnet.go`, строки 173-176
**Проблема:** пустая строка попадёт в `cidrs` и уронит refresh через parse error.
**Требование /AGENTS.md:** фильтрация route target.

**Текущий код:**
```go
if rt.Via == nil {
    cidrs = append(cidrs, rt.Target)
}
```

**Исправление:**
```go
if rt.Via == nil && strings.TrimSpace(rt.Target) != "" {
    cidrs = append(cidrs, rt.Target)
}
```

### [MED] Нет explicit `ERROR` логирования для `ErrUnauthorized`

**Файл:** `ztnet.go`, строки 145-152
**Проблема:** refresh просто возвращает error; различение unauthorized/temporary недоступно.
**Требование /AGENTS.md:** unauthorized должен логироваться как ERROR.

**Текущий код:**
```go
if err != nil { refreshCount...; return err }
```

**Исправление:**
```go
if errors.Is(err, ErrUnauthorized) {
  clog.Errorf("ztnet: unauthorized against API")
} else {
  clog.Warningf("ztnet: refresh failed: %v", err)
}
```

### [MED] `getJSON` не делает drain body для non-decode веток

**Файл:** `api.go`, строки 56-68
**Проблема:** при status>=400 body не читается/не дренируется до reuse.
**Требование /AGENTS.md:** корректное закрытие и reuse keep-alive.

**Текущий код:**
```go
if resp.StatusCode >= 400 { return fmt.Errorf(...) }
```

**Исправление:**
```go
_, _ = io.Copy(io.Discard, resp.Body)
```

### [MED] `go.mod` содержит прямую зависимость на `prometheus/client_golang`

**Файл:** `go.mod`, строка 9
**Проблема:** policy требует использовать CoreDNS metrics package как входную точку.
**Требование /AGENTS.md:** dependency policy / metrics policy.

**Текущий код:**
```go
github.com/prometheus/client_golang v1.20.5
```

**Исправление:**
```go
// после миграции на plugin/metrics и tidy — убрать прямой require
```

### [MED] Недостаточный набор тестов по security/correctness

**Файл:** `ztnet_test.go`
**Проблема:** отсутствует значимая часть обязательных тестов из чеклиста.
**Требование /AGENTS.md:** maintain/improve coverage; набор обязательных тестов.

**Текущий код:**
```go
// есть только часть базовых тестов
```

**Исправление:**
```go
// добавить отсутствующие тесты из раздела "Проверка тестов" ниже
```

### [LOW] Отсутствуют godoc-комментарии на большинстве публичных сущностей

**Файл:** `access.go`, `secret.go`, `api.go`, `cache.go`
**Проблема:** публичные типы/функции без комментов.
**Требование /AGENTS.md:** публичные типы и функции с godoc-комментариями на английском.

**Текущий код:**
```go
func NewAllowedNets(cidrs []string) ...
func LoadToken(cfg TokenConfig) ...
type Member struct ...
```

**Исправление:**
```go
// NewAllowedNets parses CIDRs and appends mandatory loopback ranges.
```

### [LOW] Тест `TestCacheSnapshot` проверяет мутируемость, а не иммутабельность

**Файл:** `ztnet_test.go`, строки 35-43
**Проблема:** ожидание построено на том, что snapshot видит внешнюю мутацию карты; это против требований immutable snapshot.
**Требование /AGENTS.md:** проверить что old snapshot не мутируется.

**Текущий код:**
```go
a["a."][0] = net.ParseIP("10.0.0.2")
if got := rc.LookupA("a.")[0].String(); got != "10.0.0.2" { ... }
```

**Исправление:**
```go
if got := rc.LookupA("a.")[0].String(); got != "10.0.0.1" { ... }
```

### [LOW] Нет теста на `TypeSOA` и DNS-SD custom domain

**Файл:** `ztnet_test.go`
**Проблема:** неполное покрытие важных DNS ветвей.
**Требование /AGENTS.md:** обязательные `TestServeDNS_SOA`, `TestServeDNS_DNSSD_Custom`.

**Исправление:**
```go
// добавить отдельные test cases на SOA и search_domain != zone
```

### [LOW] Неверная команда линтера в регламенте запуска

**Файл:** процесс аудита
**Проблема:** `--out-format` не совместим с текущим golangci-lint.
**Исправление:**
```bash
golangci-lint run ./...
# или: golangci-lint run --output.text.path stdout ./...
```

## Проверка тестов

Отсутствуют или неполны следующие тесты (по чеклисту):

- `TestLoadToken_File_Empty`, `TestLoadToken_File_Missing`, `TestLoadToken_Env_Unset`, `TestLoadToken_HotRotation`.
- `TestCacheLen`, `TestCacheIsAllowed`, `TestCache_SetIsAllowed_Atomic`.
- `TestComputeRFC4193`, `TestCompute6plane`, `TestComputeRFC4193_Invalid` (с эталонными векторами).
- `TestAllowedNets_Contains_IPv4`, `_IPv6`, `_Reject_External`, `_Loopback_Always`, `_InvalidCIDR` (разделённые по кейсам).
- `TestExtractSourceIP_TCP`.
- `TestServeDNS_REFUSED_NilSrcIP`, `TestServeDNS_Allowed_ZT`, `TestServeDNS_Allowed_Loopback`, `TestServeDNS_NilAllowlist_StrictOff`, `TestServeDNS_NilAllowlist_StrictOn`.
- `TestRefresh_AutoCIDRFromRoutes`, `TestRefresh_SkipsViaRoutes`, `TestRefresh_StaleAllowedOnBuildError`.
- `TestServeDNS_AAAA`, `TestServeDNS_ANY`, `TestServeDNS_SOA`, `TestServeDNS_OutOfZone`, `TestServeDNS_NoQuestion`, `TestServeDNS_UnknownType`, `TestServeDNS_DNSSD_TXT`, `TestServeDNS_DNSSD_Custom`, `TestServeDNS_ShortName_Hit`, `_Miss`, `_Off`, `TestIsBareName`.
- `TestFetchMembers_OnlyAuthorized`, `_401`, `_500_ThenOK`, `_Timeout`, `TestRefresh_TokenRotation`, `TestRefresh_StaleOnError` (частично есть, но не покрывает все сценарии).

## Результат после исправлений

```text
go test -race ./...   → PASS

golangci-lint run     → OK (0 issues)

govulncheck ./...     → FAIL (15 vulnerabilities detected)
```
