package ztnet

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/miekg/dns"
)

func init() { plugin.Register("ztnet", setup) }

func setup(c *caddy.Controller) error {
	cfg, err := parse(c)
	if err != nil {
		return plugin.Error("ztnet", err)
	}
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: cfg.Timeout / 2}).DialContext,
		TLSHandshakeTimeout:   cfg.Timeout / 2,
		ResponseHeaderTimeout: cfg.Timeout,
		IdleConnTimeout:       90 * time.Second,
	}
	p := &ZtnetPlugin{zone: cfg.Zone, cfg: cfg, cache: NewRecordCache(), api: &APIClient{BaseURL: cfg.APIURL, NetworkID: cfg.NetworkID, HTTPClient: &http.Client{Transport: tr, Timeout: cfg.Timeout}, MaxRetries: cfg.MaxRetries}}
	p.start(context.Background())
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		p.Next = next
		return p
	})
	c.OnFinalShutdown(func() error {
		p.stop()
		return nil
	})
	clog.Infof("ztnet: configured zone %s (version %s)", cfg.Zone, PluginVersion)
	return nil
}

func parse(c *caddy.Controller) (Config, error) {
	cfg := Config{TTL: 60, Refresh: 30 * time.Second, Timeout: 5 * time.Second, MaxRetries: 3, AutoAllowZT: true}
	tokenSources := 0
	for c.Next() {
		for c.NextBlock() {
			k := c.Val()
			args := c.RemainingArgs()
			if len(args) == 0 {
				return cfg, fmt.Errorf("%s requires value", k)
			}
			switch k {
			case "api_url":
				cfg.APIURL = args[0]
			case "network_id":
				cfg.NetworkID = args[0]
			case "zone":
				cfg.Zone = dns.Fqdn(strings.ToLower(args[0]))
			case "token_file":
				tokenSources++
				cfg.Token = TokenConfig{Source: TokenSourceFile, Value: args[0]}
			case "token_env":
				tokenSources++
				cfg.Token = TokenConfig{Source: TokenSourceEnv, Value: args[0]}
			case "api_token":
				tokenSources++
				cfg.Token = TokenConfig{Source: TokenSourceInline, Value: args[0]}
				clog.Warning("ztnet: using inline api_token is for development only")
			case "auto_allow_zt":
				v, err := strconv.ParseBool(args[0])
				if err != nil {
					return cfg, fmt.Errorf("auto_allow_zt parse: %w", err)
				}
				cfg.AutoAllowZT = v
			case "allowed_networks":
				cfg.AllowedCIDRs = append(cfg.AllowedCIDRs, args...)
			case "ttl":
				v, err := strconv.Atoi(args[0])
				if err != nil {
					return cfg, fmt.Errorf("ttl parse: %w", err)
				}
				cfg.TTL = uint32(v)
			case "refresh":
				v, err := time.ParseDuration(args[0])
				if err != nil {
					return cfg, fmt.Errorf("refresh parse: %w", err)
				}
				cfg.Refresh = v
			case "timeout":
				v, err := time.ParseDuration(args[0])
				if err != nil {
					return cfg, fmt.Errorf("timeout parse: %w", err)
				}
				cfg.Timeout = v
			case "max_retries":
				v, err := strconv.Atoi(args[0])
				if err != nil {
					return cfg, fmt.Errorf("max_retries parse: %w", err)
				}
				if v < 0 {
					return cfg, fmt.Errorf("max_retries must be >= 0, got %d", v)
				}
				cfg.MaxRetries = v
			case "strict_start":
				v, err := strconv.ParseBool(args[0])
				if err != nil {
					return cfg, fmt.Errorf("strict_start parse: %w", err)
				}
				cfg.StrictStart = v
			case "search_domain":
				cfg.SearchDomain = args[0]
			case "allow_short_names":
				v, err := strconv.ParseBool(args[0])
				if err != nil {
					return cfg, fmt.Errorf("allow_short_names parse: %w", err)
				}
				cfg.AllowShort = v
			default:
				return cfg, fmt.Errorf("unknown option %s", k)
			}
		}
	}
	cfg.Zone = strings.TrimSuffix(strings.ToLower(cfg.Zone), ".") + "."
	if cfg.APIURL == "" || cfg.NetworkID == "" || cfg.Zone == "." {
		return cfg, fmt.Errorf("api_url, network_id and zone are required")
	}
	if err := validateNetworkID(cfg.NetworkID); err != nil {
		return cfg, fmt.Errorf("network_id parse: %w", err)
	}
	if tokenSources != 1 {
		return cfg, fmt.Errorf("exactly one token source required")
	}
	if cfg.SearchDomain == "" {
		cfg.SearchDomain = cfg.Zone
	}
	cfg.SearchDomain = dns.Fqdn(strings.ToLower(cfg.SearchDomain))
	return cfg, nil
}

func validateNetworkID(networkID string) error {
	if len(networkID) != 16 {
		return fmt.Errorf("must be exactly 16 hex characters, got length %d", len(networkID))
	}
	for _, ch := range networkID {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
			return fmt.Errorf("must contain only hex characters")
		}
	}
	return nil
}
