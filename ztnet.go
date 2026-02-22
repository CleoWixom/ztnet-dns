package ztnet

import (
	"context"
	"fmt"
	"net"
	"strings"
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

func (p *ZtnetPlugin) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	if len(r.Question) == 0 {
		return dns.RcodeServerFailure, nil
	}
	q := r.Question[0]
	qname := strings.ToLower(q.Name)
	if !dns.IsSubDomain(p.zone, qname) {
		return plugin.NextOrFailure(p.Name(), p.Next, ctx, w, r)
	}
	src := extractSourceIP(w)
	if !p.cache.IsAllowed(src) {
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
	switch q.Qtype {
	case dns.TypeA:
		for _, ip := range p.cache.LookupA(qname) {
			m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: p.cfg.TTL}, A: ip})
		}
	case dns.TypeAAAA:
		for _, ip := range p.cache.LookupAAAA(qname) {
			m.Answer = append(m.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: p.cfg.TTL}, AAAA: ip})
		}
	case dns.TypeANY:
		for _, ip := range p.cache.LookupA(qname) {
			m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: p.cfg.TTL}, A: ip})
		}
		for _, ip := range p.cache.LookupAAAA(qname) {
			m.Answer = append(m.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: p.cfg.TTL}, AAAA: ip})
		}
	case dns.TypeSOA:
		m.Answer = append(m.Answer, p.soaRecord())
	case dns.TypeTXT:
		if qname == "_dns-sd._udp."+p.zone && p.cfg.SearchDomain != "" {
			txt := fmt.Sprintf("path=%s", dns.Fqdn(strings.ToLower(p.cfg.SearchDomain)))
			m.Answer = append(m.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: p.cfg.TTL}, Txt: []string{txt}})
		}
	}

	if len(m.Answer) == 0 {
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
		Ns: "ns1." + p.zone, Mbox: "hostmaster." + p.zone, Serial: uint32(time.Now().Unix()), Refresh: 3600, Retry: 600, Expire: 86400, Minttl: p.cfg.TTL}
}

func (p *ZtnetPlugin) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	go func() {
		_ = p.refresh(ctx)
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
	members, err := p.api.FetchMembers(ctx, token)
	if err != nil {
		refreshCount.WithLabelValues(p.zone, "error").Inc()
		return err
	}
	netinfo, err := p.api.FetchNetwork(ctx, token)
	if err != nil {
		refreshCount.WithLabelValues(p.zone, "error").Inc()
		return err
	}
	a, aaaa := make(map[string][]net.IP), make(map[string][]net.IP)
	for _, m := range members {
		names := []string{dns.Fqdn(strings.ToLower(strings.ReplaceAll(m.Name, " ", "_")) + "." + p.zone), dns.Fqdn(strings.ToLower(m.NodeID) + "." + p.zone)}
		for _, ipStr := range m.IPAssignments {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			for _, n := range names {
				if ip.To4() != nil {
					a[n] = append(a[n], ip)
				} else {
					aaaa[n] = append(aaaa[n], ip)
				}
			}
		}
	}
	cidrs := append([]string{}, p.cfg.AllowedCIDRs...)
	if p.cfg.AutoAllowZT {
		for _, rt := range netinfo.Config.Routes {
			if rt.Via == nil {
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
