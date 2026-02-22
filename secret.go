package ztnet

import (
	"fmt"
	"os"
	"strings"
)

const (
	TokenSourceFile   = "file"
	TokenSourceEnv    = "env"
	TokenSourceInline = "inline"
)

// TokenConfig defines token source and source value.
type TokenConfig struct {
	Source string
	Value  string
}

// LoadToken resolves token from file/env/inline config.
func LoadToken(cfg TokenConfig) (string, error) {
	switch cfg.Source {
	case TokenSourceFile:
		b, err := os.ReadFile(cfg.Value)
		if err != nil {
			return "", fmt.Errorf("token_file read: %w", err)
		}
		t := strings.TrimSpace(string(b))
		if t == "" {
			return "", fmt.Errorf("token_file empty")
		}
		return t, nil
	case TokenSourceEnv:
		t := strings.TrimSpace(os.Getenv(cfg.Value))
		if t == "" {
			return "", fmt.Errorf("token_env empty: %s", cfg.Value)
		}
		return t, nil
	case TokenSourceInline:
		t := strings.TrimSpace(cfg.Value)
		if t == "" {
			return "", fmt.Errorf("api_token empty")
		}
		return t, nil
	default:
		return "", fmt.Errorf("unknown token source: %s", cfg.Source)
	}
}
