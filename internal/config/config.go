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
	"strconv"
)

// Config holds all runtime settings.
type Config struct {
	Addr         string
	DatabaseURL  string
	MasterKey    []byte // PARE_MASTER_KEY, 32 bytes, wraps per-company DEKs
	ShieldKey    []byte // PARE_SHIELD_KEY, 32 bytes, optional (MCP boundary)
	MCPKey       string // PARE_MCP_KEY, gates the MCP endpoint (optional)
	MCPMaxOre    int64  // per-write ceiling for AI-posted amounts (öre; 0 = unlimited)
	GotenbergURL string
	LogLevel     string
}

// Load reads and validates configuration.
func Load() (*Config, error) {
	c := &Config{
		Addr:         env("PARE_ADDR", ":8080"),
		DatabaseURL:  os.Getenv("PARE_DATABASE_URL"),
		MCPKey:       os.Getenv("PARE_MCP_KEY"),
		MCPMaxOre:    envInt64("PARE_MCP_MAX_ORE", 50_000_000), // 500 000 SEK per AI write
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

	if c.MCPKey != "" && len(c.MCPKey) < 16 {
		return nil, fmt.Errorf("config: PARE_MCP_KEY must be at least 16 characters")
	}

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

func envInt64(k string, def int64) int64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
