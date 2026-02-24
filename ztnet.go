package ztnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/miekg/dns"
)

type Config struct {
	APIURL       string
	NetworkID    string
	Zone         string
	Token        TokenConfig
	TTL          uint32
	Refresh      time.Duration
	Timeout      time.Duration
	MaxRetries   int
	AutoAllowZT  bool
	AllowedCIDRs []string
	StrictStart  bool
	SearchDomain string
	AllowShort   bool
}

type ZtnetPlugin struct {
	Next   plugin.Handler
	zone   string
	cfg    Config
	cache  *RecordCache
	api    *APIClient
	cancel context.CancelFunc
}

func (p *ZtnetPlugin) Name() string { return "ztnet" }

func isBareName(qname string) bool {
	return strings.Count(qname, ".") == 1
}

func buildA(name string, ttl uint32, ip net.IP) *dns.A {
	return &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: ip.To4()}
}

func buildAAAA(name string, ttl uint32, ip net.IP) *dns.AAAA {
	return &dns.AAAA{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: ttl}, AAAA: ip}
}

func (p *ZtnetPlugin) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	if len(r.Question) == 0 {
		return dns.RcodeServerFailure, nil
	}
	q := r.Question[0]
	qname := strings.ToLower(q.Name)
	lookupName := qname
	inZone := dns.IsSubDomain(p.zone, qname)
	if p.cfg.AllowShort && isBareName(qname) {
		lookupName = qname[:len(qname)-1] + "." + p.zone
		inZone = true
	}
	if !inZone {
		return plugin.NextOrFailure(p.Name(), p.Next, ctx, w, r)
	}
	src := extractSourceIP(w)
	if !p.cache.IsAllowed(src, p.cfg.StrictStart) {
		clog.Warningf("ztnet: REFUSED query name=%s type=%d src=%v", qname, q.Qtype, src)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeRefused
		_ = w.WriteMsg(m)
		refusedCount.WithLabelValues(p.zone).Inc()
		requestCount.WithLabelValues(p.zone, dns.RcodeToString[dns.RcodeRefused]).Inc()
		return dns.RcodeRefused, nil
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	if q.Qtype == dns.TypeTXT && qname == "_dns-sd._udp."+p.zone && p.cfg.SearchDomain != "" {
		txt := fmt.Sprintf("path=%s", dns.Fqdn(strings.ToLower(p.cfg.SearchDomain)))
		m.Answer = append(m.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: p.cfg.TTL}, Txt: []string{txt}})
		_ = w.WriteMsg(m)
		requestCount.WithLabelValues(p.zone, dns.RcodeToString[dns.RcodeSuccess]).Inc()
		return dns.RcodeSuccess, nil
	}

	aRecords := p.cache.LookupA(lookupName)
	aaaaRecords := p.cache.LookupAAAA(lookupName)
	foundName := len(aRecords) > 0 || len(aaaaRecords) > 0

	switch q.Qtype {
	case dns.TypeA:
		for _, ip := range aRecords {
			m.Answer = append(m.Answer, buildA(lookupName, p.cfg.TTL, ip))
		}
	case dns.TypeAAAA:
		for _, ip := range aaaaRecords {
			m.Answer = append(m.Answer, buildAAAA(lookupName, p.cfg.TTL, ip))
		}
	case dns.TypeANY:
		for _, ip := range aRecords {
			m.Answer = append(m.Answer, buildA(lookupName, p.cfg.TTL, ip))
		}
		for _, ip := range aaaaRecords {
			m.Answer = append(m.Answer, buildAAAA(lookupName, p.cfg.TTL, ip))
		}
	case dns.TypeSOA:
		if lookupName == p.zone {
			m.Answer = append(m.Answer, p.soaRecord())
		}
	default:
		// unknown type => NOERROR/NODATA for existing name, NXDOMAIN otherwise
	}

	if len(m.Answer) == 0 {
		if foundName {
			m.Rcode = dns.RcodeSuccess
			_ = w.WriteMsg(m)
			requestCount.WithLabelValues(p.zone, dns.RcodeToString[dns.RcodeSuccess]).Inc()
			return dns.RcodeSuccess, nil
		}
		if p.cfg.AllowShort && isBareName(qname) {
			return plugin.NextOrFailure(p.Name(), p.Next, ctx, w, r)
		}
		m.Rcode = dns.RcodeNameError
		m.Ns = append(m.Ns, p.soaRecord())
		_ = w.WriteMsg(m)
		requestCount.WithLabelValues(p.zone, dns.RcodeToString[dns.RcodeNameError]).Inc()
		return dns.RcodeNameError, nil
	}

	_ = w.WriteMsg(m)
	requestCount.WithLabelValues(p.zone, dns.RcodeToString[dns.RcodeSuccess]).Inc()
	return dns.RcodeSuccess, nil
}

func (p *ZtnetPlugin) soaRecord() dns.RR {
	return &dns.SOA{Hdr: dns.RR_Header{Name: p.zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: p.cfg.TTL},
		Ns: "ns1." + p.zone, Mbox: "hostmaster." + p.zone, Serial: p.cache.Serial(), Refresh: 3600, Retry: 600, Expire: 86400, Minttl: p.cfg.TTL}
}

func (p *ZtnetPlugin) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	go func() {
		defer func() {
			if r := recover(); r != nil {
				clog.Errorf("ztnet: panic in refresh goroutine: %v", r)
			}
		}()
		if err := p.refresh(ctx); err != nil {
			clog.Warningf("ztnet: initial refresh failed for zone %s: %v", p.zone, err)
		}
		t := time.NewTicker(p.cfg.Refresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := p.refresh(ctx); err != nil {
					clog.Warningf("ztnet: refresh failed: %v", err)
				}
			}
		}
	}()
}

func (p *ZtnetPlugin) stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *ZtnetPlugin) refresh(ctx context.Context) error {
	token, err := LoadToken(p.cfg.Token)
	if err != nil {
		tokenReload.WithLabelValues(p.zone, p.cfg.Token.Source, "error").Inc()
		refreshCount.WithLabelValues(p.zone, "error").Inc()
		return fmt.Errorf("load token: %w", err)
	}
	tokenReload.WithLabelValues(p.zone, p.cfg.Token.Source, "ok").Inc()
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	var members []Member
	var netinfo NetworkInfo
	var membersErr, netErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		members, membersErr = p.api.FetchMembers(ctx, token)
	}()
	go func() {
		defer wg.Done()
		netinfo, netErr = p.api.FetchNetwork(ctx, token)
	}()
	wg.Wait()
	if membersErr != nil {
		refreshCount.WithLabelValues(p.zone, "error").Inc()
		if errors.Is(membersErr, ErrUnauthorized) {
			clog.Errorf("ztnet: unauthorized against API members endpoint")
		}
		return membersErr
	}
	if netErr != nil {
		refreshCount.WithLabelValues(p.zone, "error").Inc()
		if errors.Is(netErr, ErrUnauthorized) {
			clog.Errorf("ztnet: unauthorized against API network endpoint")
		}
		return netErr
	}

	a, aaaa := make(map[string][]net.IP), make(map[string][]net.IP)
	for _, m := range members {
		nodeID := strings.ToLower(strings.TrimSpace(m.NodeID))
		if nodeID == "" {
			clog.Warningf("ztnet: member %q has empty nodeID, skipping", m.Name)
			continue
		}
		names := []string{dns.Fqdn(nodeID + "." + p.zone)}
		if m.Name != "" {
			normalizedName := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(m.Name), " ", "_"))
			candidate := normalizedName + "." + p.zone
			if _, ok := dns.IsDomainName(candidate); ok {
				names = append(names, dns.Fqdn(candidate))
			} else {
				clog.Warningf("ztnet: member name %q is not a valid DNS label, skipping name record", m.Name)
			}
		}
		for _, ipStr := range m.IPAssignments {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			for _, n := range names {
				if ip.To4() != nil {
					a[n] = append(a[n], ip.To4())
				} else {
					aaaa[n] = append(aaaa[n], ip)
				}
			}
		}
	}
	cidrs := append([]string{}, p.cfg.AllowedCIDRs...)
	if p.cfg.AutoAllowZT {
		for _, rt := range netinfo.Config.Routes {
			if rt.Via == nil && strings.TrimSpace(rt.Target) != "" {
				cidrs = append(cidrs, rt.Target)
			}
		}
	}
	allowed, err := NewAllowedNets(cidrs)
	if err != nil {
		refreshCount.WithLabelValues(p.zone, "error").Inc()
		return fmt.Errorf("build allowlist: %w", err)
	}
	p.cache.Set(a, aaaa, allowed)
	ac, aaaac := p.cache.Counts()
	entriesGauge.WithLabelValues(p.zone, "A").Set(float64(ac))
	entriesGauge.WithLabelValues(p.zone, "AAAA").Set(float64(aaaac))
	refreshCount.WithLabelValues(p.zone, "ok").Inc()
	return nil
}
