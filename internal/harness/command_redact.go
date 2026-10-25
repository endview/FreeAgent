package harness

import (
	"os"
	"sort"
	"strings"
)

func knownCommandSecrets(keys *KeyStore) []string {
	seen := map[string]bool{}
	if keys != nil {
		if profiles, err := keys.List(); err == nil {
			for _, profile := range profiles {
				if key, err := keys.Resolve(profile.ID); err == nil && key.secret != "" {
					seen[key.secret] = true
				}
			}
		}
	}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok && sensitiveCommandEnvironmentName(name) && len(value) >= minRedactedEnvValue {
			seen[value] = true
		}
	}
	secrets := make([]string, 0, len(seen))
	for secret := range seen {
		if len(secret) <= 16384 {
			secrets = append(secrets, secret)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}
func commandRedact(text string, secrets []string) string {
	for _, secret := range secrets {
		text = strings.ReplaceAll(text, secret, redactedText)
	}
	return text
}

func (t *commandTail) redactChunk(stream int, text string, final bool) string {
	text = commandRedact(t.secretPending[stream]+text, t.secrets)
	keep := 0
	if !final {
		for _, secret := range t.secrets {
			for n := min(len(secret)-1, len(text)); n > keep; n-- {
				if strings.HasSuffix(text, secret[:n]) {
					keep = n
					break
				}
			}
		}
	}
	t.secretPending[stream] = text[len(text)-keep:]
	return text[:len(text)-keep]
}
