package ztnet

import (
	"context"
	"fmt"
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
	p := &ZtnetPlugin{zone: cfg.Zone, cfg: cfg, cache: NewRecordCache(), api: &APIClient{BaseURL: cfg.APIURL, NetworkID: cfg.NetworkID, HTTPClient: &http.Client{}, MaxRetries: cfg.MaxRetries}}
	p.start(context.Background())
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		p.Next = next
		return p
	})
	c.OnFinalShutdown(func() error {
		p.stop()
		return nil
	})
	clog.Infof("ztnet: configured zone %s", cfg.Zone)
	return nil
}

func parse(c *caddy.Controller) (Config, error) {
	cfg := Config{TTL: 60, Refresh: 30 * time.Second, Timeout: 5 * time.Second, MaxRetries: 3, AutoAllowZT: true}
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
				cfg.Token = TokenConfig{Source: "file", Value: args[0]}
			case "token_env":
				cfg.Token = TokenConfig{Source: "env", Value: args[0]}
			case "api_token":
				cfg.Token = TokenConfig{Source: "inline", Value: args[0]}
				clog.Warning("ztnet: using inline api_token is for development only")
			case "auto_allow_zt":
				v, _ := strconv.ParseBool(args[0])
				cfg.AutoAllowZT = v
			case "allowed_networks":
				cfg.AllowedCIDRs = append(cfg.AllowedCIDRs, args[0])
			case "ttl":
				v, _ := strconv.Atoi(args[0])
				cfg.TTL = uint32(v)
			case "refresh":
				v, _ := time.ParseDuration(args[0])
				cfg.Refresh = v
			case "timeout":
				v, _ := time.ParseDuration(args[0])
				cfg.Timeout = v
			case "max_retries":
				v, _ := strconv.Atoi(args[0])
				cfg.MaxRetries = v
			case "strict_start":
				v, _ := strconv.ParseBool(args[0])
				cfg.StrictStart = v
			case "search_domain":
				cfg.SearchDomain = args[0]
			case "allow_short_names":
				v, _ := strconv.ParseBool(args[0])
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
	if cfg.Token.Source == "" {
		return cfg, fmt.Errorf("one token source required")
	}
	return cfg, nil
}
