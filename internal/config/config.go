// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package config loads Pare's runtime configuration from PARE_* environment
// variables. Required secrets have no defaults: the server refuses to start
// without them.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
)

// Config holds all runtime settings.
type Config struct {
	Addr         string
	DatabaseURL  string
	MasterKey    []byte // PARE_MASTER_KEY, 32 bytes, wraps per-company DEKs
	ShieldKey    []byte // PARE_SHIELD_KEY, 32 bytes, optional (MCP boundary)
	ShieldHint   string
	GotenbergURL string
	LogLevel     string
}

// Load reads and validates configuration.
func Load() (*Config, error) {
	c := &Config{
		Addr:         env("PARE_ADDR", ":8080"),
		DatabaseURL:  os.Getenv("PARE_DATABASE_URL"),
		ShieldHint:   env("PARE_SHIELD_HINT_LEVEL", "bucketed"),
		GotenbergURL: env("PARE_GOTENBERG_URL", "http://gotenberg:3000"),
		LogLevel:     env("PARE_LOG_LEVEL", "info"),
	}
	mk, err := key("PARE_MASTER_KEY", true)
	if err != nil {
		return nil, err
	}
	c.MasterKey = mk

	sk, err := key("PARE_SHIELD_KEY", false)
	if err != nil {
		return nil, err
	}
	c.ShieldKey = sk

	return c, nil
}

func key(name string, required bool) ([]byte, error) {
	v := os.Getenv(name)
	if v == "" {
		if required {
			return nil, fmt.Errorf("config: %s is required", name)
		}
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("config: %s must be base64: %w", name, err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("config: %s must decode to 32 bytes, got %d", name, len(raw))
	}
	return raw, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
