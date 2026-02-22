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

type RecordCache struct {
	snap atomic.Value
}

func NewRecordCache() *RecordCache {
	rc := &RecordCache{}
	rc.snap.Store(cacheSnapshot{a: map[string][]net.IP{}, aaaa: map[string][]net.IP{}, allowed: &AllowedNets{}})
	return rc
}

func (r *RecordCache) Set(a, aaaa map[string][]net.IP, allowed *AllowedNets) {
	if a == nil {
		a = map[string][]net.IP{}
	}
	if aaaa == nil {
		aaaa = map[string][]net.IP{}
	}
	if allowed == nil {
		allowed = &AllowedNets{}
	}
	r.snap.Store(cacheSnapshot{a: a, aaaa: aaaa, allowed: allowed})
}

func (r *RecordCache) load() cacheSnapshot             { return r.snap.Load().(cacheSnapshot) }
func (r *RecordCache) LookupA(name string) []net.IP    { return r.load().a[name] }
func (r *RecordCache) LookupAAAA(name string) []net.IP { return r.load().aaaa[name] }
func (r *RecordCache) IsAllowed(ip net.IP) bool        { return r.load().allowed.Contains(ip) }
func (r *RecordCache) Counts() (int, int) {
	s := r.load()
	return len(s.a), len(s.aaaa)
}
