// Package config loads application configuration from the environment.
package config

import "os"

// Config holds runtime configuration for the application.
type Config struct {
	Addr string // HTTP listen address, e.g. ":8080"
	Env  string // deployment environment, e.g. "development", "production"

	// MoonshotAPIKey authenticates requests to the Moonshot AI (Kimi) chat
	// completions API — see internal/kimi and internal/llm. It is a
	// secret: never log or print it verbatim (internal/secretmask masks it
	// wherever it might otherwise leak into logs or terminal output).
	MoonshotAPIKey string

	// MoonshotBaseURL overrides internal/kimi's default API endpoint, e.g.
	// to point at a proxy or a local server in tests. Empty means "use the
	// client's built-in default".
	MoonshotBaseURL string

	// DeepSeekAPIKey authenticates requests to the DeepSeek chat
	// completions API — see internal/deepseek and internal/llm. Same
	// secrecy rules as MoonshotAPIKey.
	DeepSeekAPIKey string

	// DeepSeekBaseURL overrides internal/deepseek's default API endpoint,
	// same purpose as MoonshotBaseURL.
	DeepSeekBaseURL string
}

// Load reads configuration from environment variables, falling back to
// sensible defaults for local development.
func Load() (*Config, error) {
	cfg := &Config{
		Addr:            getEnv("ADDR", ":8080"),
		Env:             getEnv("APP_ENV", "development"),
		MoonshotAPIKey:  os.Getenv("MOONSHOT_API_KEY"),
		MoonshotBaseURL: os.Getenv("MOONSHOT_BASE_URL"),
		DeepSeekAPIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekBaseURL: os.Getenv("DEEPSEEK_BASE_URL"),
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
