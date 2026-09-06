// Package kimi holds the Moonshot AI (Kimi) provider's endpoint. The
// request/response contract itself is generic OpenAI-compatible chat
// completions, implemented once in internal/llm and shared with every
// other provider (see internal/deepseek) — only the base URL and API key
// differ. Documented in ./api-examples/kimi-completions.txt at the repo
// root.
package kimi

// DefaultBaseURL is the Moonshot AI chat completions endpoint, used for
// all "kimi-*" models.
const DefaultBaseURL = "https://api.moonshot.ai/v1/chat/completions"
