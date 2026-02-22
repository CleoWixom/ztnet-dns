package ztnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

type Member struct {
	NodeID        string   `json:"nodeId"`
	Name          string   `json:"name"`
	Authorized    bool     `json:"authorized"`
	IPAssignments []string `json:"ipAssignments"`
}

type Route struct {
	Target string  `json:"target"`
	Via    *string `json:"via"`
}

type NetworkInfo struct {
	Config struct {
		Routes []Route `json:"routes"`
	} `json:"config"`
}

type APIClient struct {
	BaseURL    string
	NetworkID  string
	HTTPClient *http.Client
	MaxRetries int
}

func (c *APIClient) getJSON(ctx context.Context, token, path string, out any) error {
	url := strings.TrimRight(c.BaseURL, "/") + path
	for i := 0; i <= c.MaxRetries; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("x-ztnet-auth", token)
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			if i == c.MaxRetries {
				return fmt.Errorf("GET %s: %w", path, err)
			}
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return ErrUnauthorized
		}
		if resp.StatusCode >= 500 {
			if i == c.MaxRetries {
				return fmt.Errorf("GET %s status %d", path, resp.StatusCode)
			}
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
			continue
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("GET %s status %d", path, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("retry loop exhausted")
}

func (c *APIClient) FetchMembers(ctx context.Context, token string) ([]Member, error) {
	var members []Member
	path := fmt.Sprintf("/api/v1/network/%s/member", c.NetworkID)
	if err := c.getJSON(ctx, token, path, &members); err != nil {
		return nil, fmt.Errorf("fetch members: %w", err)
	}
	out := make([]Member, 0, len(members))
	for _, m := range members {
		if m.Authorized {
			out = append(out, m)
		}
	}
	return out, nil
}

func (c *APIClient) FetchNetwork(ctx context.Context, token string) (NetworkInfo, error) {
	var n NetworkInfo
	path := fmt.Sprintf("/api/v1/network/%s", c.NetworkID)
	if err := c.getJSON(ctx, token, path, &n); err != nil {
		return NetworkInfo{}, fmt.Errorf("fetch network: %w", err)
	}
	return n, nil
}
