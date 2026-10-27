// Package config reads process configuration from the environment.
package config

import (
	"encoding/json"
	"fmt"
	"net"
)

// Config is the process configuration shared by all subcommands.
type Config struct {
	HTTPAddr         string
	MinAppVersion    string
	LatestAppVersion string
	CrisisPhones     []CrisisPhone
}

// CrisisPhone is a crisis line shown to clients. Numbers come from configuration
// per country, never from code.
type CrisisPhone struct {
	Number string `json:"number"`
	Label  string `json:"label"`
}

// Load builds a Config from getenv (os.Getenv in production, a map in tests).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:         getenv("BIRGE_HTTP_ADDR"),
		MinAppVersion:    getenv("BIRGE_MIN_APP_VERSION"),
		LatestAppVersion: getenv("BIRGE_LATEST_APP_VERSION"),
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("BIRGE_HTTP_ADDR: %w", err)
	}
	if cfg.MinAppVersion == "" {
		cfg.MinAppVersion = "0.0.0"
	}
	if cfg.LatestAppVersion == "" {
		cfg.LatestAppVersion = cfg.MinAppVersion
	}
	if raw := getenv("BIRGE_CRISIS_PHONES"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.CrisisPhones); err != nil {
			return Config{}, fmt.Errorf("BIRGE_CRISIS_PHONES: %w", err)
		}
	}
	return cfg, nil
}
