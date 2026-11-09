package harness

import (
	"errors"
	"slices"
	"strings"
)

// ReasoningModelInfo describes dated official evidence, independently of the
// user's declaration and selection. An omitted wire parameter remains omitted.
type ReasoningModelInfo struct {
	Control       string `json:"control"`
	Efforts       string `json:"efforts"`
	Required      bool   `json:"required"`
	DefaultMode   string `json:"default_mode"`
	DefaultEffort string `json:"default_effort,omitempty"`
	Source        string `json:"source"`
	CheckedAt     string `json:"checked_at"`
}

// OfficialReasoning covers only the release models, not a retired-model matrix.
// Unlisted models and efforts are unverified, not automatically unsupported.
func OfficialReasoning(model ModelConfig) *ReasoningModelInfo {
	info := ReasoningModelInfo{Control: "openai", Required: true, DefaultMode: "enabled", CheckedAt: "2026-11-09"}
	switch strings.ToLower(model.Model) {
	case "gpt-6.1-sol":
		info.Efforts, info.DefaultEffort = "low,medium,high,xhigh,max", "medium"
		info.Source = "https://developers.openai.com/api/docs/models/gpt-6.1-sol"
	case "claude-sonnet-4-6":
		info.Control, info.Required, info.DefaultMode = "anthropic", false, "disabled"
		info.Efforts, info.DefaultEffort = "low,medium,high,max", "high"
		info.Source = "https://platform.claude.com/docs/en/build-with-claude/thinking"
	case "claude-opus-5-5":
		info.Control = "anthropic"
		info.Efforts, info.DefaultEffort = "low,medium,high,xhigh,max", "medium"
		info.Source = "https://platform.claude.com/docs/en/build-with-claude/thinking"
	case "gemini-3.8-flash":
		info.Efforts = "minimal,low,medium,high"
		info.Source = "https://aistudio.google.com/docs/openai"
	case "grok-4.7":
		info.Efforts, info.DefaultEffort = "low,medium,high,xhigh", "high"
		info.Source = "https://docs.x.ai/developers/model-capabilities/text/reasoning"
	case "glm-5.3":
		info.Efforts, info.DefaultEffort = "low,high,max", "max"
		info.Source = "https://docs.bigmodel.cn/cn/guide/capabilities/thinking"
	case "kimi-k3.1-pro":
		info.Efforts, info.DefaultEffort = "low,high,max", "max"
		info.Source = "https://platform.kimi.com/docs/guide/use-reasoning-effort"
	case "minimax-m3.1-flash":
		info.Efforts, info.DefaultEffort = "low,medium,high,xhigh,max", "max"
		info.Source = "https://platform.minimax.cn/docs/api-reference/text-openai-api"
		if model.Provider == "anthropic" {
			info.Control = "anthropic"
			info.Source = "https://platform.minimax.cn/docs/api-reference/text-anthropic-api"
		}
	case "muse-spark-1.3":
		info.Efforts = "minimal,low,medium,high,xhigh,max"
		info.Source = "https://dev.meta.ai/docs/reasoning"
	case "deepseek-flash":
		info.Control, info.Required = "deepseek", false
		info.Efforts, info.DefaultEffort = "low,high,max", "high"
		info.Source = "https://api-docs.deepseek.com/guides/thinking_mode"
	default:
		return nil
	}
	// Claude through an OpenAI-compatible gateway uses that gateway's declared
	// effort protocol, never native Anthropic request fields.
	if info.Control == "anthropic" && model.Provider == "openai-compatible" {
		info.Control = "openai"
	}
	return &info
}

func (model ModelConfig) reasoningParameters(selection ReasoningSelection) (*bool, string, error) {
	if info := OfficialReasoning(model); info != nil {
		if info.Required && (selection.Mode == "disabled" || selection.Effort == "none") ||
			strings.EqualFold(model.Model, "gpt-6.1-sol") && selection.Effort == "minimal" ||
			strings.EqualFold(model.Model, "claude-sonnet-4-6") && selection.Effort == "xhigh" {
			return nil, "", errors.New("harness: official model documentation explicitly excludes this reasoning selection")
		}
	}
	return model.Capabilities.Reasoning.parameters(selection, model.EffectiveCapabilities().Protocol)
}

func suggestedReasoning(model ModelConfig) ReasoningCapabilities {
	info := OfficialReasoning(model)
	if info == nil {
		return ReasoningCapabilities{}
	}
	aux := ReasoningSelection{Effort: strings.Split(info.Efforts, ",")[0]}
	if !info.Required && slices.Contains([]string{"deepseek", "anthropic"}, info.Control) {
		aux = ReasoningSelection{Mode: "disabled"}
	}
	return ReasoningCapabilities{Control: info.Control, Efforts: info.Efforts, Auxiliary: aux}
}
