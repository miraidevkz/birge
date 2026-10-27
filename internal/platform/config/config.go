// Package config reads process configuration from the environment.
package config

import (
	"fmt"
	"net"
)

// Config is the process configuration shared by all subcommands.
type Config struct {
	HTTPAddr string
}

// Load builds a Config from getenv (os.Getenv in production, a map in tests).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{HTTPAddr: getenv("BIRGE_HTTP_ADDR")}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("BIRGE_HTTP_ADDR: %w", err)
	}
	return cfg, nil
}
