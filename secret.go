package ztnet

import (
	"fmt"
	"os"
	"strings"
)

type TokenConfig struct {
	Source string // file|env|inline
	Value  string
}

func LoadToken(cfg TokenConfig) (string, error) {
	switch cfg.Source {
	case "file":
		b, err := os.ReadFile(cfg.Value)
		if err != nil {
			return "", fmt.Errorf("token_file read: %w", err)
		}
		t := strings.TrimSpace(string(b))
		if t == "" {
			return "", fmt.Errorf("token_file empty")
		}
		return t, nil
	case "env":
		t := strings.TrimSpace(os.Getenv(cfg.Value))
		if t == "" {
			return "", fmt.Errorf("token_env empty: %s", cfg.Value)
		}
		return t, nil
	case "inline":
		t := strings.TrimSpace(cfg.Value)
		if t == "" {
			return "", fmt.Errorf("api_token empty")
		}
		return t, nil
	default:
		return "", fmt.Errorf("unknown token source: %s", cfg.Source)
	}
}
