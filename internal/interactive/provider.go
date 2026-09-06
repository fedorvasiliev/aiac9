package interactive

import (
	"fmt"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/deepseek"
	"github.com/fedorvasiliev/aiac9/internal/kimi"
	"github.com/fedorvasiliev/aiac9/internal/llm"
)

// resolveClient picks the provider for model, per CLAUDE.md: models
// containing "deepseek" use DeepSeek's own contract (internal/deepseek);
// everything else (including the built-in default, "kimi-k2.6") uses
// Moonshot AI (internal/kimi) — both are plain internal/llm clients,
// differing only in base URL and API key.
func resolveClient(model string, cfg *config.Config) (*llm.Client, error) {
	if strings.Contains(strings.ToLower(model), "deepseek") {
		if cfg.DeepSeekAPIKey == "" {
			return nil, fmt.Errorf("DEEPSEEK_API_KEY is not set")
		}
		baseURL := deepseek.DefaultBaseURL
		if cfg.DeepSeekBaseURL != "" {
			baseURL = cfg.DeepSeekBaseURL
		}
		return llm.NewClient(baseURL, cfg.DeepSeekAPIKey), nil
	}

	if cfg.MoonshotAPIKey == "" {
		return nil, fmt.Errorf("MOONSHOT_API_KEY is not set")
	}
	baseURL := kimi.DefaultBaseURL
	if cfg.MoonshotBaseURL != "" {
		baseURL = cfg.MoonshotBaseURL
	}
	return llm.NewClient(baseURL, cfg.MoonshotAPIKey), nil
}
