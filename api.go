package ztnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

// Member describes a ZTNET member response object.
type Member struct {
	NodeID        string   `json:"nodeId"`
	Name          string   `json:"name"`
	Authorized    bool     `json:"authorized"`
	IPAssignments []string `json:"ipAssignments"`
}

func parseJSONFlexibleString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s), nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), nil
	}
	return "", fmt.Errorf("unsupported JSON type")
}

// UnmarshalJSON accepts multiple ZTNET member payload variants.
func (m *Member) UnmarshalJSON(data []byte) error {
	var aux struct {
		NodeIDCamel   json.RawMessage `json:"nodeId"`
		NodeIDLower   json.RawMessage `json:"nodeid"`
		ID            json.RawMessage `json:"id"`
		Address       json.RawMessage `json:"address"`
		Name          string          `json:"name"`
		Authorized    bool            `json:"authorized"`
		IPAssignments []string        `json:"ipAssignments"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	m.Name = aux.Name
	m.Authorized = aux.Authorized
	m.IPAssignments = aux.IPAssignments

	candidates := []json.RawMessage{aux.NodeIDCamel, aux.NodeIDLower, aux.ID, aux.Address}
	for _, raw := range candidates {
		v, err := parseJSONFlexibleString(raw)
		if err != nil {
			return fmt.Errorf("member node id decode: %w", err)
		}
		if v != "" {
			m.NodeID = v
			return nil
		}
	}

	m.NodeID = ""
	return nil
}

// NetworkRoute describes a route in network config.
type NetworkRoute struct {
	Target string  `json:"target"`
	Via    *string `json:"via"`
}

// NetworkInfo describes network-level ZTNET information.
type NetworkInfo struct {
	Config struct {
		Routes []NetworkRoute `json:"routes"`
	} `json:"config"`
}

// APIClient calls ZTNET API endpoints.
type APIClient struct {
	BaseURL    string
	NetworkID  string
	HTTPClient *http.Client
	MaxRetries int
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()

	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *APIClient) getJSON(ctx context.Context, token, path string, out any) error {
	url := strings.TrimRight(c.BaseURL, "/") + path
	for i := 0; i <= c.MaxRetries; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("build request %s: %w", path, err)
		}
		req.Header.Set("x-ztnet-auth", token)

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if i == c.MaxRetries {
				return fmt.Errorf("GET %s: %w", path, err)
			}
			if err := waitRetry(ctx, time.Duration(i+1)*100*time.Millisecond); err != nil {
				return err
			}
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return ErrUnauthorized
		}
		if resp.StatusCode >= 500 {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if i == c.MaxRetries {
				return fmt.Errorf("GET %s status %d", path, resp.StatusCode)
			}
			if err := waitRetry(ctx, time.Duration(i+1)*100*time.Millisecond); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode >= 400 {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return fmt.Errorf("GET %s status %d", path, resp.StatusCode)
		}
		const maxResponseBody = 10 << 20
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("decode %s: %w", path, err)
		}
		_ = resp.Body.Close()
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
