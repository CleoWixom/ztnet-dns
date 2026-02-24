package ztnet

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type nextOK struct{}

func (nextOK) Name() string { return "next" }
func (nextOK) ServeDNS(context.Context, dns.ResponseWriter, *dns.Msg) (int, error) {
	return dns.RcodeSuccess, nil
}

func mustAllowed(t *testing.T, cidrs ...string) *AllowedNets {
	t.Helper()
	a, err := NewAllowedNets(cidrs)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCacheSnapshot(t *testing.T) {
	rc := NewRecordCache()
	a := map[string][]net.IP{"a.": {net.ParseIP("10.0.0.1")}}
	rc.Set(a, nil, mustAllowed(t, "10.0.0.0/8"))
	a["a."][0] = net.ParseIP("10.0.0.2")
	if got := rc.LookupA("a.")[0].String(); got != "10.0.0.1" {
		t.Fatalf("expected immutable snapshot value, got %s", got)
	}
}

func TestCacheConcurrency(t *testing.T) {
	rc := NewRecordCache()
	rc.Set(map[string][]net.IP{"a.": {net.ParseIP("10.0.0.1")}}, nil, mustAllowed(t, "10.0.0.0/8"))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = rc.LookupA("a.")
				_ = rc.IsAllowed(net.ParseIP("10.0.0.9"), false)
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rc.Set(map[string][]net.IP{"a.": {net.ParseIP("10.0.0.2")}}, nil, mustAllowed(t, "10.0.0.0/8"))
		}(i)
	}
	wg.Wait()
}

func TestAllowedNets_Contains(t *testing.T) {
	a := mustAllowed(t, "10.10.0.0/16")
	if !a.Contains(net.ParseIP("10.10.1.2")) || !a.Contains(net.ParseIP("127.0.0.1")) {
		t.Fatal("expected allowed")
	}
	if a.Contains(net.ParseIP("8.8.8.8")) {
		t.Fatal("unexpected allowed")
	}
	if !a.Contains(net.ParseIP("::ffff:10.10.1.2")) {
		t.Fatal("expected ipv4-mapped ipv6 to be allowed")
	}
}

func TestExtractSourceIP_UDP(t *testing.T) {
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}}
	if got := extractSourceIP(rw).String(); got != "8.8.8.8" {
		t.Fatal(got)
	}
}

func TestExtractSourceIP_TCP(t *testing.T) {
	rw := &fakeRW{remoteAddr: &net.TCPAddr{IP: net.ParseIP("8.8.4.4"), Port: 53}}
	if got := extractSourceIP(rw).String(); got != "8.8.4.4" {
		t.Fatal(got)
	}
}

type fakeRW struct {
	remoteAddr net.Addr
	msg        *dns.Msg
}

func (f *fakeRW) LocalAddr() net.Addr       { return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53} }
func (f *fakeRW) RemoteAddr() net.Addr      { return f.remoteAddr }
func (f *fakeRW) WriteMsg(m *dns.Msg) error { f.msg = m; return nil }
func (f *fakeRW) Write([]byte) (int, error) { return 0, nil }
func (f *fakeRW) Close() error              { return nil }
func (f *fakeRW) TsigStatus() error         { return nil }
func (f *fakeRW) TsigTimersOnly(bool)       {}
func (f *fakeRW) Hijack()                   {}

func basePlugin(t *testing.T) *ZtnetPlugin {
	rc := NewRecordCache()
	rc.Set(map[string][]net.IP{"server01.zt.example.com.": {net.ParseIP("10.147.20.5")}}, map[string][]net.IP{"server01.zt.example.com.": {net.ParseIP("fd00::1")}}, mustAllowed(t, "10.147.0.0/16"))
	return &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{TTL: 60, SearchDomain: "zt.example.com.", AllowShort: true}, cache: rc, Next: nextOK{}}
}

func TestServeDNS_A(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.zt.example.com.", dns.TypeA)
	rcode, err := p.ServeDNS(context.Background(), rw, req)
	if err != nil || rcode != dns.RcodeSuccess || len(rw.msg.Answer) != 1 {
		t.Fatalf("rcode=%d err=%v ans=%d", rcode, err, len(rw.msg.Answer))
	}
}

func TestServeDNS_AAAA(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.zt.example.com.", dns.TypeAAAA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeSuccess || len(rw.msg.Answer) != 1 {
		t.Fatalf("rcode=%d ans=%d", rcode, len(rw.msg.Answer))
	}
}

func TestServeDNS_NXDOMAIN(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("missing.zt.example.com.", dns.TypeA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeNameError || len(rw.msg.Ns) == 0 {
		t.Fatal("expected nxdomain+soa")
	}
}

func TestServeDNS_NXDOMAIN_SerialStableUntilRefresh(t *testing.T) {
	var round atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/network/n/member":
			if round.Load() == 0 {
				_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.147.20.5"]}]`))
			} else {
				_, _ = w.Write([]byte(`[{"nodeId":"b","name":"srv2","authorized":true,"ipAssignments":["10.147.20.6"]}]`))
			}
			return
		case "/api/v1/network/n":
			_, _ = w.Write([]byte(`{"config":{"routes":[]}}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	p := &ZtnetPlugin{
		zone:  "zt.example.com.",
		cfg:   Config{TTL: 60, Token: TokenConfig{Source: "inline", Value: "tok"}, Timeout: time.Second, AllowedCIDRs: []string{"10.147.0.0/16"}},
		cache: NewRecordCache(),
		api:   &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 0},
		Next:  nextOK{},
	}
	if err := p.refresh(context.Background()); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	rw1 := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("missing.zt.example.com.", dns.TypeA)
	if rcode, err := p.ServeDNS(context.Background(), rw1, req); err != nil || rcode != dns.RcodeNameError {
		t.Fatalf("first NXDOMAIN failed: rcode=%d err=%v", rcode, err)
	}
	soa1, ok := rw1.msg.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("expected SOA in authority, got %T", rw1.msg.Ns[0])
	}

	rw2 := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 2222}}
	if rcode, err := p.ServeDNS(context.Background(), rw2, req); err != nil || rcode != dns.RcodeNameError {
		t.Fatalf("second NXDOMAIN failed: rcode=%d err=%v", rcode, err)
	}
	soa2, ok := rw2.msg.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("expected SOA in authority, got %T", rw2.msg.Ns[0])
	}
	if soa1.Serial != soa2.Serial {
		t.Fatalf("expected stable serial without refresh, got %d and %d", soa1.Serial, soa2.Serial)
	}

	round.Store(1)
	if err := p.refresh(context.Background()); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	rw3 := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 3333}}
	if rcode, err := p.ServeDNS(context.Background(), rw3, req); err != nil || rcode != dns.RcodeNameError {
		t.Fatalf("third NXDOMAIN failed: rcode=%d err=%v", rcode, err)
	}
	soa3, ok := rw3.msg.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("expected SOA in authority, got %T", rw3.msg.Ns[0])
	}
	if soa3.Serial == soa2.Serial {
		t.Fatalf("expected serial to change after refresh, still %d", soa3.Serial)
	}
}

func TestServeDNS_UnknownType_NODATA(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.zt.example.com.", dns.TypeSRV)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeSuccess {
		t.Fatalf("expected NODATA success, got %d", rcode)
	}
}

func TestServeDNS_GlobalDNS_Passthrough(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("google.com.", dns.TypeA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeSuccess {
		t.Fatalf("expected passthrough success, got %d", rcode)
	}
}

func TestServeDNS_REFUSED_External(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.zt.example.com.", dns.TypeA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeRefused {
		t.Fatalf("got %d", rcode)
	}
}

func TestServeDNS_NilAllowlist_StrictStart(t *testing.T) {
	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{TTL: 60, StrictStart: true}, cache: NewRecordCache(), Next: nextOK{}}
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.zt.example.com.", dns.TypeA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeRefused {
		t.Fatalf("expected refused before first refresh, got %d", rcode)
	}
}

func TestLoadToken_FileEnvInline(t *testing.T) {
	d := t.TempDir()
	fp := filepath.Join(d, "tok")
	if err := os.WriteFile(fp, []byte(" abc \n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	if tok, _ := LoadToken(TokenConfig{Source: "file", Value: fp}); tok != "abc" {
		t.Fatal(tok)
	}
	t.Setenv("ZTNET_API_TOKEN", "xyz")
	if tok, _ := LoadToken(TokenConfig{Source: "env", Value: "ZTNET_API_TOKEN"}); tok != "xyz" {
		t.Fatal(tok)
	}
	if tok, _ := LoadToken(TokenConfig{Source: "inline", Value: " i "}); tok != "i" {
		t.Fatal(tok)
	}
}

func TestLoadToken_HotRotation(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(fp, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	t1, err := LoadToken(TokenConfig{Source: TokenSourceFile, Value: fp})
	if err != nil || t1 != "a" {
		t.Fatalf("%v %s", err, t1)
	}
	if err := os.WriteFile(fp, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	t2, err := LoadToken(TokenConfig{Source: TokenSourceFile, Value: fp})
	if err != nil || t2 != "b" {
		t.Fatalf("%v %s", err, t2)
	}
}

func TestLoadToken_File_Empty(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(fp, []byte(" \n\t "), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(TokenConfig{Source: TokenSourceFile, Value: fp}); err == nil {
		t.Fatal("expected empty file error")
	}
}

func TestLoadToken_File_Missing(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "missing-token")
	if _, err := LoadToken(TokenConfig{Source: TokenSourceFile, Value: fp}); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestLoadToken_Env_Unset(t *testing.T) {
	const envName = "ZTNET_TEST_UNSET_TOKEN"
	t.Setenv(envName, "")
	if _, err := LoadToken(TokenConfig{Source: TokenSourceEnv, Value: envName}); err == nil {
		t.Fatal("expected unset env error")
	}
}

func TestComputeInvalidLengths(t *testing.T) {
	if _, err := ComputeRFC4193("17d3", "efcc1b0947"); err == nil {
		t.Fatal("expected networkID length error")
	}
	if _, err := Compute6plane("17d395d8cb43a800", "ef"); err == nil {
		t.Fatal("expected nodeID length error")
	}
}

func TestComputeRFC4193(t *testing.T) {
	ip, err := ComputeRFC4193("17d395d8cb43a800", "efcc1b0947")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ip.String(); got != "fd96:3fe2:4d62:efcc:1b09:4700::" {
		t.Fatalf("unexpected RFC4193 value: %s", got)
	}
}

func TestCompute6plane(t *testing.T) {
	ip, err := Compute6plane("17d395d8cb43a800", "efcc1b0947")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ip.String(); got != "fc17:d395:d8ef:cc1b:947::" {
		t.Fatalf("unexpected 6plane value: %s", got)
	}
}

func TestAllowedNets_InvalidCIDR(t *testing.T) {
	if _, err := NewAllowedNets([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
}

func TestFetchMembers_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/network/n/member" {
			_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
			return
		}
		if r.URL.Path == "/api/v1/network/n" {
			_, _ = w.Write([]byte(`{"config":{"routes":[{"target":"10.0.0.0/24","via":null}]}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()
	c := &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 1}
	ms, err := c.FetchMembers(context.Background(), "t")
	if err != nil || len(ms) != 1 {
		t.Fatalf("%v %d", err, len(ms))
	}
}

func TestFetchMembers_OnlyAuthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/network/n/member" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`[
			{"nodeId":"a","name":"srv-a","authorized":true,"ipAssignments":["10.0.0.2"]},
			{"nodeId":"b","name":"srv-b","authorized":false,"ipAssignments":["10.0.0.3"]}
		]`))
	}))
	defer ts.Close()

	c := &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 1}
	ms, err := c.FetchMembers(context.Background(), "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms) != 1 || ms[0].NodeID != "a" {
		t.Fatalf("expected only authorized member, got %#v", ms)
	}
}

func TestFetchMembers_NodeIDNumberAndLowercaseField(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/network/n/member" {
			_, _ = w.Write([]byte(`[{"nodeid":2,"id":"abcdef01235","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	c := &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 1}
	ms, err := c.FetchMembers(context.Background(), "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("expected 1 member, got %d", len(ms))
	}
	if ms[0].NodeID != "2" {
		t.Fatalf("expected nodeID 2, got %q", ms[0].NodeID)
	}
}

func TestFetchMembers_401(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	c := &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 2}
	_, err := c.FetchMembers(context.Background(), "t")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

func TestFetchMembers_500ThenOK(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/network/n/member" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
	}))
	defer ts.Close()

	c := &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 2}
	ms, err := c.FetchMembers(context.Background(), "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("expected 1 member after retry, got %d", len(ms))
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
}

func TestFetchMembers_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	c := &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 0}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := c.FetchMembers(ctx, "t")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestRefresh_TokenRotation(t *testing.T) {
	var seenMu sync.Mutex
	seen := make([]string, 0, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		seen = append(seen, r.Header.Get("x-ztnet-auth"))
		seenMu.Unlock()
		if r.URL.Path == "/api/v1/network/n/member" {
			_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
			return
		}
		if r.URL.Path == "/api/v1/network/n" {
			_, _ = w.Write([]byte(`{"config":{"routes":[{"target":"10.0.0.0/24","via":null}]}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	tf := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tf, []byte("tok-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{Token: TokenConfig{Source: "file", Value: tf}, Timeout: time.Second}, cache: NewRecordCache(), api: &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 0}}
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tf, []byte("tok-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	seenMu.Lock()
	defer seenMu.Unlock()
	if !slices.Contains(seen, "tok-a") || !slices.Contains(seen, "tok-b") {
		t.Fatalf("expected both tokens to be used, got %v", seen)
	}
}

func TestRefresh_AutoCIDRFromRoutes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/network/n/member" {
			_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
			return
		}
		if r.URL.Path == "/api/v1/network/n" {
			_, _ = w.Write([]byte(`{"config":{"routes":[{"target":"10.55.0.0/16","via":null}]}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{Token: TokenConfig{Source: "inline", Value: "tok"}, Timeout: time.Second, AutoAllowZT: true}, cache: NewRecordCache(), api: &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 0}}
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !p.cache.IsAllowed(net.ParseIP("10.55.1.20"), true) {
		t.Fatal("expected auto route CIDR to be allowed")
	}
}

func TestRefresh_SkipsViaRoutes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/network/n/member" {
			_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
			return
		}
		if r.URL.Path == "/api/v1/network/n" {
			_, _ = w.Write([]byte(`{"config":{"routes":[{"target":"10.77.0.0/16","via":"10.0.0.1"}]}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{Token: TokenConfig{Source: "inline", Value: "tok"}, Timeout: time.Second, AutoAllowZT: true}, cache: NewRecordCache(), api: &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 0}}
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.cache.IsAllowed(net.ParseIP("10.77.1.20"), true) {
		t.Fatal("expected via route CIDR to be skipped")
	}
}

func TestRefresh_StaleAllowedOnBuildError(t *testing.T) {
	var invalid atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/network/n/member" {
			_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
			return
		}
		if r.URL.Path == "/api/v1/network/n" {
			if invalid.Load() {
				_, _ = w.Write([]byte(`{"config":{"routes":[{"target":"not-cidr","via":null}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"config":{"routes":[{"target":"10.66.0.0/16","via":null}]}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{Token: TokenConfig{Source: "inline", Value: "tok"}, Timeout: time.Second, AutoAllowZT: true}, cache: NewRecordCache(), api: &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 0}}
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	invalid.Store(true)
	if err := p.refresh(context.Background()); err == nil {
		t.Fatal("expected build allowlist error")
	}
	if !p.cache.IsAllowed(net.ParseIP("10.66.1.20"), true) {
		t.Fatal("expected stale allowlist to remain on build error")
	}
	if len(p.cache.LookupA("srv.zt.example.com.")) == 0 {
		t.Fatal("expected stale DNS records to remain on build error")
	}
}

func TestCache_SetIsAllowed_Atomic(t *testing.T) {
	rc := NewRecordCache()
	rc.Set(map[string][]net.IP{"srv.": {net.ParseIP("10.0.0.1")}}, nil, mustAllowed(t, "10.0.0.0/24"))

	for i := 0; i < 2000; i++ {
		if i%2 == 0 {
			rc.Set(map[string][]net.IP{"srv.": {net.ParseIP("10.0.0.1")}}, nil, mustAllowed(t, "10.0.0.0/24"))
		} else {
			rc.Set(map[string][]net.IP{"srv.": {net.ParseIP("10.0.1.1")}}, nil, mustAllowed(t, "10.0.1.0/24"))
		}
		s := rc.load()
		ips := s.a["srv."]
		if len(ips) == 0 {
			continue
		}
		if s.allowed == nil || !s.allowed.Contains(ips[0]) {
			t.Fatal("observed non-atomic record/allowlist snapshot")
		}
	}
}

func TestServeDNS_REFUSED_NilSrcIP(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{}
	req := new(dns.Msg)
	req.SetQuestion("server01.zt.example.com.", dns.TypeA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeRefused {
		t.Fatalf("expected REFUSED for nil src ip, got %d", rcode)
	}
}

func TestServeDNS_NilAllowlist_StrictOff(t *testing.T) {
	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{TTL: 60, StrictStart: false}, cache: NewRecordCache(), Next: nextOK{}}
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("missing.zt.example.com.", dns.TypeA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeNameError || len(rw.msg.Ns) == 0 {
		t.Fatalf("expected NXDOMAIN with SOA, got rcode=%d ns=%d", rcode, len(rw.msg.Ns))
	}
}

func TestServeDNS_ANY(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.zt.example.com.", dns.TypeANY)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeSuccess || len(rw.msg.Answer) != 2 {
		t.Fatalf("expected both A and AAAA answers, got rcode=%d answers=%d", rcode, len(rw.msg.Answer))
	}
}

func TestServeDNS_SOA(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("anything.zt.example.com.", dns.TypeSOA)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeSuccess || len(rw.msg.Answer) != 1 {
		t.Fatalf("expected SOA answer, got rcode=%d answers=%d", rcode, len(rw.msg.Answer))
	}
	if _, ok := rw.msg.Answer[0].(*dns.SOA); !ok {
		t.Fatalf("expected SOA answer, got %T", rw.msg.Answer[0])
	}
}

func TestServeDNS_OutOfZone(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("google.com.", dns.TypeA)
	rcode, err := p.ServeDNS(context.Background(), rw, req)
	if err != nil || rcode != dns.RcodeSuccess {
		t.Fatalf("expected passthrough success, got rcode=%d err=%v", rcode, err)
	}
	if rw.msg != nil {
		t.Fatal("out-of-zone query should be handled by next plugin")
	}
}

func TestServeDNS_NoQuestion(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	rcode, err := p.ServeDNS(context.Background(), rw, new(dns.Msg))
	if err != nil || rcode != dns.RcodeServerFailure {
		t.Fatalf("expected SERVFAIL, got rcode=%d err=%v", rcode, err)
	}
}

func TestServeDNS_DNSSD_TXT(t *testing.T) {
	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{TTL: 60, SearchDomain: "corp.example.com."}, cache: NewRecordCache(), Next: nextOK{}}
	p.cache.Set(nil, nil, mustAllowed(t, "10.147.0.0/16"))
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("_dns-sd._udp.zt.example.com.", dns.TypeTXT)
	rcode, _ := p.ServeDNS(context.Background(), rw, req)
	if rcode != dns.RcodeSuccess || len(rw.msg.Answer) != 1 {
		t.Fatalf("expected TXT answer, got rcode=%d answers=%d", rcode, len(rw.msg.Answer))
	}
}

func TestServeDNS_ShortName_AllowShort_OutOfZonePassthrough(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.", dns.TypeA)
	rcode, err := p.ServeDNS(context.Background(), rw, req)
	if err != nil || rcode != dns.RcodeSuccess {
		t.Fatalf("expected out-of-zone short-name passthrough, got rcode=%d err=%v", rcode, err)
	}
	if rw.msg != nil {
		t.Fatal("out-of-zone short-name should be handled by next plugin")
	}
}

func TestServeDNS_ShortName_AllowShort_MissPassthrough(t *testing.T) {
	p := basePlugin(t)
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("unknown.", dns.TypeA)
	rcode, err := p.ServeDNS(context.Background(), rw, req)
	if err != nil || rcode != dns.RcodeSuccess {
		t.Fatalf("expected passthrough for missing short-name, got rcode=%d err=%v", rcode, err)
	}
	if rw.msg != nil {
		t.Fatal("missing short-name should pass to next plugin")
	}
}

func TestServeDNS_ShortName_Off(t *testing.T) {
	p := basePlugin(t)
	p.cfg.AllowShort = false
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("server01.", dns.TypeA)
	rcode, err := p.ServeDNS(context.Background(), rw, req)
	if err != nil || rcode != dns.RcodeSuccess {
		t.Fatalf("expected out-of-zone passthrough when short names disabled, got rcode=%d err=%v", rcode, err)
	}
	if rw.msg != nil {
		t.Fatal("short name should stay out-of-zone and pass to next plugin")
	}
}

func TestServeDNS_AllowShort_DoesNotChangeInZoneMiss(t *testing.T) {
	p := basePlugin(t)
	p.cfg.AllowShort = true
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("10.147.20.9"), Port: 1111}}
	req := new(dns.Msg)
	req.SetQuestion("unknown.zt.example.com.", dns.TypeA)
	rcode, err := p.ServeDNS(context.Background(), rw, req)
	if err != nil || rcode != dns.RcodeNameError {
		t.Fatalf("expected NXDOMAIN for in-zone miss, got rcode=%d err=%v", rcode, err)
	}
	if rw.msg == nil || len(rw.msg.Ns) == 0 {
		t.Fatal("expected SOA in authority for in-zone miss")
	}
}

func TestIsBareName(t *testing.T) {
	if !isBareName("host.") {
		t.Fatal("expected host. to be bare")
	}
	if isBareName("host.zt.example.com.") {
		t.Fatal("expected fqdn with zone not to be bare")
	}
}

func TestRefresh_StaleOnAPIError(t *testing.T) {
	var fail atomic.Bool
	fail.Store(false)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/api/v1/network/n/member" {
			_, _ = w.Write([]byte(`[{"nodeId":"a","name":"srv","authorized":true,"ipAssignments":["10.0.0.2"]}]`))
			return
		}
		if r.URL.Path == "/api/v1/network/n" {
			_, _ = w.Write([]byte(`{"config":{"routes":[{"target":"10.0.0.0/24","via":null}]}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()
	tf := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tf, []byte("tok"), 0o600)
	p := &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{Token: TokenConfig{Source: "file", Value: tf}, Timeout: time.Second, AllowedCIDRs: []string{"10.0.0.0/24"}, AutoAllowZT: true}, cache: NewRecordCache(), api: &APIClient{BaseURL: ts.URL, NetworkID: "n", HTTPClient: ts.Client(), MaxRetries: 0}}
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	_ = p.refresh(context.Background())
	if len(p.cache.LookupA("srv.zt.example.com.")) == 0 {
		t.Fatal("stale cache should remain")
	}
}
