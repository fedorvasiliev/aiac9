// Package deepseek holds the DeepSeek provider's endpoint, per its official
// docs (https://api-docs.deepseek.com/, "Your First API Call"). The
// request/response contract itself is generic OpenAI-compatible chat
// completions, implemented once in internal/llm and shared with every
// other provider (see internal/kimi) — only the base URL and API key
// differ.
package deepseek

// DefaultBaseURL is DeepSeek's chat completions endpoint, used for all
// "deepseek-*" models.
const DefaultBaseURL = "https://api.deepseek.com/chat/completions"
