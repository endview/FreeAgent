package harness

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// ReasoningSelection distinguishes provider defaults from an explicit off.
// The same selection is frozen in a Run and mapped only by its provider.
type ReasoningSelection struct {
	Mode   string `json:"mode,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type ReasoningCapabilities struct {
	Control   string             `json:"control,omitempty"`
	Efforts   string             `json:"efforts,omitempty"`
	Default   ReasoningSelection `json:"default,omitempty,omitzero"`
	Auxiliary ReasoningSelection `json:"auxiliary,omitempty,omitzero"`
}

func (caps ReasoningCapabilities) efforts() []string {
	if caps.Efforts == "" {
		return nil
	}
	return strings.Split(caps.Efforts, ",")
}

func (caps ReasoningCapabilities) validate(protocol string) error {
	if caps.Control != "" && caps.Control != "none" && caps.Control != "openai" && caps.Control != "deepseek" {
		return errors.New("harness: unsupported reasoning control")
	}
	if len(caps.Efforts) > 128 || len(caps.efforts()) > 8 {
		return errors.New("harness: too many reasoning efforts")
	}
	seen := map[string]bool{}
	for _, effort := range caps.efforts() {
		if seen[effort] || !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, effort) || caps.Control == "deepseek" && !slices.Contains([]string{"low", "high", "max"}, effort) {
			return errors.New("harness: invalid reasoning efforts")
		}
		seen[effort] = true
	}
	if caps.Control == "" || caps.Control == "none" {
		if caps.Efforts != "" || caps.Default != (ReasoningSelection{}) || caps.Auxiliary != (ReasoningSelection{}) {
			return errors.New("harness: reasoning options require an explicit supported control")
		}
		return nil
	}
	if protocol == "local" {
		return errors.New("harness: local model does not support provider reasoning parameters")
	}
	if _, _, err := caps.parameters(caps.Default, protocol); err != nil {
		return err
	}
	_, _, err := caps.parameters(caps.Auxiliary, protocol)
	return err
}

// parameters returns a Chat toggle (when supported) and an effort. An empty
// result deliberately omits wire fields rather than claiming thinking is off.
func (caps ReasoningCapabilities) parameters(selection ReasoningSelection, protocol string) (*bool, string, error) {
	if selection.Mode != "" && selection.Mode != "default" && selection.Mode != "enabled" && selection.Mode != "disabled" {
		return nil, "", errors.New("harness: invalid reasoning mode")
	}
	if caps.Control == "" || caps.Control == "none" {
		if selection != (ReasoningSelection{}) && selection != (ReasoningSelection{Mode: "default"}) {
			return nil, "", errors.New("harness: model has no declared reasoning control")
		}
		return nil, "", nil
	}
	if selection.Mode == "default" && selection.Effort != "" {
		return nil, "", errors.New("harness: provider default cannot include a reasoning effort")
	}
	if selection.Mode == "disabled" {
		if selection.Effort != "" {
			return nil, "", errors.New("harness: disabled reasoning cannot include an effort")
		}
		if caps.Control == "deepseek" {
			if protocol == "responses" {
				return nil, "none", nil
			}
			off := false
			return &off, "", nil
		}
		if !slices.Contains(caps.efforts(), "none") {
			return nil, "", errors.New("harness: this model does not support disabling reasoning")
		}
		return nil, "none", nil
	}
	if selection.Effort != "" && (!slices.Contains(caps.efforts(), selection.Effort) || selection.Effort == "none") {
		return nil, "", errors.New("harness: reasoning effort is not supported by this model")
	}
	if selection.Mode == "enabled" && selection.Effort == "" && (caps.Control != "deepseek" || protocol == "responses") {
		return nil, "", errors.New("harness: enabling reasoning requires a declared effort for this protocol")
	}
	if caps.Control == "deepseek" && protocol != "responses" && (selection.Mode == "enabled" || selection.Effort != "") {
		on := true
		return &on, selection.Effort, nil
	}
	return nil, selection.Effort, nil
}

// ChatContinuation is protocol data scoped to the exact endpoint, credential
// profile and model that produced it. It is not user-visible response text.
type ChatContinuation struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

func chatContinuationSource(base, key, model string) string {
	return contentHash(mustJSON([]string{"chat/reasoning_content/1", base, key, model}))
}

func validChatContinuation(saved *ChatContinuation) bool {
	return saved != nil && len(saved.Source) == 64 && len(saved.Content) <= 1<<20 && utf8.ValidString(saved.Content)
}
