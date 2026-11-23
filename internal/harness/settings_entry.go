package harness

// SettingsEntryRules is the explicit, default-deny Telegram policy for
// instance-level proposals. A direct candidate still needs a full diff check.
type SettingsEntryRule struct {
	TelegramPropose bool   `json:"telegram_private_propose"`
	TelegramDecide  string `json:"telegram_private_decide,omitempty"`
}

func SettingsEntryRules(kind string) SettingsEntryRule {
	switch kind {
	case ControlSetRuntimeLimits:
		return SettingsEntryRule{true, "auto_compact_percent"}
	case ControlSetDefaultModel:
		return SettingsEntryRule{true, "same_saved_provider"}
	case ControlSetAttachmentPolicy, ControlSetAutoPolicy, ControlSetTool:
		return SettingsEntryRule{TelegramPropose: true}
	default:
		return SettingsEntryRule{}
	}
}
