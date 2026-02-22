package ztnet

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if got := rc.LookupA("a.")[0].String(); got != "10.0.0.2" {
		t.Fatalf("expected current snapshot value, got %s", got)
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
				_ = rc.IsAllowed(net.ParseIP("10.0.0.9"))
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rc.Set(map[string][]net.IP{"a.": {net.ParseIP(fmt.Sprintf("10.0.0.%d", i+2))}}, nil, mustAllowed(t, "10.0.0.0/8"))
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
}

func TestExtractSourceIP_UDP(t *testing.T) {
	rw := &fakeRW{remoteAddr: &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}}
	if got := extractSourceIP(rw).String(); got != "8.8.8.8" {
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
	return &ZtnetPlugin{zone: "zt.example.com.", cfg: Config{TTL: 60, SearchDomain: "zt.example.com"}, cache: rc, Next: nextOK{}}
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
