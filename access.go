package ztnet

import (
	"fmt"
	"net"
	"strings"

	"github.com/miekg/dns"
)

// AllowedNets stores allowed source CIDRs.
type AllowedNets struct {
	nets []*net.IPNet
}

func NewAllowedNets(cidrs []string) (*AllowedNets, error) {
	out := &AllowedNets{nets: make([]*net.IPNet, 0, len(cidrs)+2)}
	// always allow loopback
	for _, c := range []string{"127.0.0.0/8", "::1/128"} {
		_, n, _ := net.ParseCIDR(c)
		out.nets = append(out.nets, n)
	}
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("parse CIDR %q: %w", cidr, err)
		}
		out.nets = append(out.nets, n)
	}
	return out, nil
}

func (a *AllowedNets) Contains(ip net.IP) bool {
	if a == nil || ip == nil {
		return false
	}
	for _, n := range a.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func extractSourceIP(w dns.ResponseWriter) net.IP {
	if w == nil || w.RemoteAddr() == nil {
		return nil
	}
	if a, ok := w.RemoteAddr().(*net.UDPAddr); ok {
		return a.IP
	}
	if a, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP
	}
	host, _, err := net.SplitHostPort(w.RemoteAddr().String())
	if err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(w.RemoteAddr().String())
}
