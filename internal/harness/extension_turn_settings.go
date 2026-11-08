package harness

import (
	"encoding/json"
	"errors"
	"maps"
)

type ExtensionSettingOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ExtensionTurnSetting struct {
	Key     string                   `json:"key"`
	Label   string                   `json:"label"`
	Options []ExtensionSettingOption `json:"options"`
}

// Reuse the extension's adjustable settings and JSON Schema. Only finite
// string choices are exposed in the composer; no code or UI runtime is loaded.
func extensionTurnSettings(manifest ExtensionManifest) []ExtensionTurnSetting {
	var schema struct {
		Properties map[string]struct {
			Type  string   `json:"type"`
			Title string   `json:"title"`
			Enum  []string `json:"enum"`
			OneOf []struct {
				Const string `json:"const"`
				Title string `json:"title"`
			} `json:"oneOf"`
		} `json:"properties"`
	}
	if json.Unmarshal(manifest.SettingsSchema, &schema) != nil {
		return nil
	}
	var result []ExtensionTurnSetting
	for _, key := range manifest.ModelSettings {
		p := schema.Properties[key]
		if p.Type != "string" || len(p.Enum)+len(p.OneOf) < 1 || len(p.Enum)+len(p.OneOf) > 16 {
			continue
		}
		field := ExtensionTurnSetting{Key: key, Label: p.Title}
		if field.Label == "" {
			field.Label = key
		}
		valid := len(field.Label) <= 256
		for _, value := range p.Enum {
			field.Options = append(field.Options, ExtensionSettingOption{Value: value, Label: value})
		}
		for _, item := range p.OneOf {
			label := item.Title
			if label == "" {
				label = item.Const
			}
			field.Options = append(field.Options, ExtensionSettingOption{Value: item.Const, Label: label})
		}
		seen := map[string]bool{}
		for _, option := range field.Options {
			if option.Value == "" || len(option.Value) > 128 || len(option.Label) > 256 || seen[option.Value] {
				valid = false
			}
			seen[option.Value] = true
		}
		if valid {
			result = append(result, field)
		}
	}
	return result
}

func (service *Service) validateCapabilitySettings(selection CapabilitySelection) error {
	if len(selection.Settings) == 0 {
		return nil
	}
	config := service.currentConfig()
	registry, err := scanExtensions(service.Store.DataDir(), config.Workspace, config)
	if err != nil {
		return err
	}
	for id, settings := range selection.Settings {
		definition, installed := registry.Extensions[id]
		if !installed || registry.Errors[id] != nil || !config.Extensions.Enabled[id] || !contains(selection.Extensions, id) {
			return errors.New("harness: turn settings require an installed, enabled and selected extension")
		}
		fields := extensionTurnSettings(definition.Manifest)
		merged := maps.Clone(config.Extensions.Settings[id])
		if merged == nil {
			merged = map[string]any{}
		}
		for key, value := range settings {
			allowed := false
			for _, field := range fields {
				if field.Key == key {
					for _, option := range field.Options {
						allowed = allowed || option.Value == value
					}
				}
			}
			if !allowed {
				return errors.New("harness: undeclared extension turn setting or value")
			}
			merged[key] = value
		}
		resolved, err := resolveToolSchema(definition.Manifest.SettingsSchema)
		if err != nil || resolved.Validate(merged) != nil {
			return errors.New("harness: extension turn settings do not satisfy its schema")
		}
	}
	return nil
}
