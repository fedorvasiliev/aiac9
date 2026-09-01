// Package config loads application configuration from the environment.
package config

import "os"

// Config holds runtime configuration for the application.
type Config struct {
	Addr string // HTTP listen address, e.g. ":8080"
	Env  string // deployment environment, e.g. "development", "production"
}

// Load reads configuration from environment variables, falling back to
// sensible defaults for local development.
func Load() (*Config, error) {
	cfg := &Config{
		Addr: getEnv("ADDR", ":8080"),
		Env:  getEnv("APP_ENV", "development"),
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
