package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// DefaultConfigPath is where the MCP server config lives by default —
// CLAUDE.md's "## Работа с MCP" shows this exact "mcpServers" shape.
const DefaultConfigPath = "mcp.json"

// ServerConfig is one entry of the config's "mcpServers" map: how to reach
// that server. Type "http" (CLAUDE.md's later config shape) connects over
// Streamable HTTP at URL; anything else (empty, or "stdio") launches
// Command (with Args/Env) as a local process speaking MCP over its
// stdin/stdout, CLAUDE.md's original shape.
type ServerConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// Config is the parsed config file.
type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfig reads and parses the MCP config at path. A missing file is
// not an error — it yields an empty Config, since having no MCP servers
// configured at all is valid (CLAUDE.md doesn't require it).
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}
