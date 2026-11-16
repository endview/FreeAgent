package harness

import (
	"net/url"
	"strings"

	"github.com/endview/freeagent/internal/webaccess"
)

// An explicit declaration can opt into an otherwise unverified compatible
// relay. Automatic selection is limited to the adapters' official contracts.
func nativeSearchAvailable(model ModelConfig, base string, explicit bool) bool {
	if model.Capabilities.WebSearch != "supported" {
		return false
	}
	u, _ := url.Parse(base)
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	if model.Provider == "anthropic" {
		return explicit || host == "api.anthropic.com"
	}
	if model.EffectiveCapabilities().Protocol != "responses" {
		return false
	}
	if host == "api.deepseek.com" {
		return model.Model == "deepseek-flash"
	}
	return explicit || host == "api.openai.com"
}

func nativeCitationLink(rawURL, title, key string) string {
	if key != "" && strings.Contains(rawURL, key) {
		return ""
	}
	u, err := webaccess.PublicURL(rawURL)
	if err != nil {
		return ""
	}
	title = redact(truncateUTF8Bytes(title, 256), key)
	if title == "" {
		title = u.Hostname()
	}
	title = strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ", "\r", " ").Replace(title)
	link := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(u.String())
	return " [" + title + "](" + link + ")"
}

func validateServerToolUsage(usage *ServerToolUsage) error {
	if usage == nil {
		return nil
	}
	for _, count := range []*int64{usage.SearchRequests, usage.FetchRequests} {
		if count != nil && (*count < 0 || *count > 10000) {
			return &CallError{Category: "protocol", Message: "invalid server tool usage"}
		}
	}
	return nil
}
