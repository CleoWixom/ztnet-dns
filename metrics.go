package ztnet

import (
	coremetrics "github.com/coredns/coredns/plugin/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	requestCount = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "coredns_ztnet_requests_total", Help: "DNS requests handled"}, []string{"zone", "rcode"})
	refusedCount = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "coredns_ztnet_refused_total", Help: "REFUSED responses"}, []string{"zone"})
	refreshCount = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "coredns_ztnet_cache_refresh_total", Help: "Refresh attempts"}, []string{"zone", "status"})
	entriesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "coredns_ztnet_cache_entries", Help: "Cache entry count"}, []string{"zone", "type"})
	tokenReload  = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "coredns_ztnet_token_reload_total", Help: "Token reload attempts"}, []string{"zone", "source", "status"})
)

func init() {
	registry := coremetrics.New("")
	registry.MustRegister(requestCount)
	registry.MustRegister(refusedCount)
	registry.MustRegister(refreshCount)
	registry.MustRegister(entriesGauge)
	registry.MustRegister(tokenReload)
}
