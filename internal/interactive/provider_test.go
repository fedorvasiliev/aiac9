package interactive

import (
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/deepseek"
	"github.com/fedorvasiliev/aiac9/internal/kimi"
)

func TestResolveClient_RoutesByModelName(t *testing.T) {
	cfg := &config.Config{MoonshotAPIKey: "moon-key", DeepSeekAPIKey: "deep-key"}

	c, err := resolveClient("kimi-k2.6", cfg)
	if err != nil {
		t.Fatalf("resolveClient(kimi): %v", err)
	}
	if c.BaseURL != kimi.DefaultBaseURL || c.APIKey != "moon-key" {
		t.Fatalf("kimi client = %+v, want BaseURL=%q APIKey=%q", c, kimi.DefaultBaseURL, "moon-key")
	}

	c, err = resolveClient("deepseek-chat", cfg)
	if err != nil {
		t.Fatalf("resolveClient(deepseek): %v", err)
	}
	if c.BaseURL != deepseek.DefaultBaseURL || c.APIKey != "deep-key" {
		t.Fatalf("deepseek client = %+v, want BaseURL=%q APIKey=%q", c, deepseek.DefaultBaseURL, "deep-key")
	}

	// Case-insensitive match.
	c, err = resolveClient("DeepSeek-Reasoner", cfg)
	if err != nil {
		t.Fatalf("resolveClient(DeepSeek-Reasoner): %v", err)
	}
	if c.BaseURL != deepseek.DefaultBaseURL {
		t.Fatalf("expected case-insensitive routing to DeepSeek, got BaseURL=%q", c.BaseURL)
	}
}

func TestResolveClient_BaseURLOverrides(t *testing.T) {
	cfg := &config.Config{
		MoonshotAPIKey:  "moon-key",
		MoonshotBaseURL: "http://moon.local",
		DeepSeekAPIKey:  "deep-key",
		DeepSeekBaseURL: "http://deep.local",
	}

	c, _ := resolveClient("kimi-k2.6", cfg)
	if c.BaseURL != "http://moon.local" {
		t.Fatalf("BaseURL = %q, want the MoonshotBaseURL override", c.BaseURL)
	}

	c, _ = resolveClient("deepseek-chat", cfg)
	if c.BaseURL != "http://deep.local" {
		t.Fatalf("BaseURL = %q, want the DeepSeekBaseURL override", c.BaseURL)
	}
}

func TestResolveClient_MissingKeyIsReportedPerProvider(t *testing.T) {
	_, err := resolveClient("kimi-k2.6", &config.Config{})
	if err == nil || !strings.Contains(err.Error(), "MOONSHOT_API_KEY") {
		t.Fatalf("err = %v, want it to mention MOONSHOT_API_KEY", err)
	}

	_, err = resolveClient("deepseek-chat", &config.Config{})
	if err == nil || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Fatalf("err = %v, want it to mention DEEPSEEK_API_KEY", err)
	}
}
