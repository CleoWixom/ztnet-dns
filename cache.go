package ztnet

import (
	"net"
	"sync/atomic"
)

type cacheSnapshot struct {
	a       map[string][]net.IP
	aaaa    map[string][]net.IP
	allowed *AllowedNets
}

// RecordCache is an atomic immutable snapshot cache for DNS records and ACLs.
type RecordCache struct {
	snap atomic.Value
}

// NewRecordCache creates an initialized cache with empty maps and nil allowlist.
func NewRecordCache() *RecordCache {
	rc := &RecordCache{}
	rc.snap.Store(cacheSnapshot{a: map[string][]net.IP{}, aaaa: map[string][]net.IP{}, allowed: nil})
	return rc
}

func cloneRecords(in map[string][]net.IP) map[string][]net.IP {
	if in == nil {
		return map[string][]net.IP{}
	}
	out := make(map[string][]net.IP, len(in))
	for k, ips := range in {
		cloned := make([]net.IP, len(ips))
		copy(cloned, ips)
		out[k] = cloned
	}
	return out
}

// Set atomically publishes a new snapshot.
func (r *RecordCache) Set(a, aaaa map[string][]net.IP, allowed *AllowedNets) {
	r.snap.Store(cacheSnapshot{a: cloneRecords(a), aaaa: cloneRecords(aaaa), allowed: allowed})
}

func (r *RecordCache) load() cacheSnapshot             { return r.snap.Load().(cacheSnapshot) }
func (r *RecordCache) LookupA(name string) []net.IP    { return r.load().a[name] }
func (r *RecordCache) LookupAAAA(name string) []net.IP { return r.load().aaaa[name] }

// IsAllowed returns source allow result, honoring strict_start when allowlist is nil.
func (r *RecordCache) IsAllowed(ip net.IP, strictStart bool) bool {
	s := r.load()
	if s.allowed == nil {
		return !strictStart
	}
	return s.allowed.Contains(ip)
}

func (r *RecordCache) Counts() (int, int) {
	s := r.load()
	return len(s.a), len(s.aaaa)
}
