package harness

import "regexp"

var commonSecretPattern = regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{12,}|\b[0-9]{6,}:[a-z0-9_-]{20,}|\b(?:password|api[_-]?key|authorization|bot[_-]?token)\s*[:=]\s*["']?[^\s"'<>]{8,}|-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)

func (service *Service) redactSecrets(text string) string {
	config := cloneConfig(service.currentConfig())
	if config.Providers == nil {
		config.Providers = map[string]bool{}
	}
	if service.Keys != nil {
		if profiles, err := service.Keys.List(); err == nil {
			for _, p := range profiles {
				config.Providers[p.ID] = true
			}
		}
	}
	text = commandRedact(text, knownCommandSecrets(service.Keys, config))
	text = commandRedact(text, knownCommandSecrets(NewTaskService(service).ChannelKeys(), config))
	return commonSecretPattern.ReplaceAllString(text, "[credential removed]")
}
