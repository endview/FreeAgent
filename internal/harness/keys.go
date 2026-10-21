package harness

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Key sources. A none source sends no Authorization header and is limited to
// loopback addresses, where local model servers usually need no key.
const (
	KeySourceSaved = "saved"
	KeySourceEnv   = "env"
	KeySourceNone  = "none"
)

const (
	KeyProtocolOpenAICompatible = "openai-compatible"
	KeyProtocolAnthropic        = "anthropic"
)

const (
	keysFileVersion        = 1
	maxKeysFileBytes       = 1 << 20
	maxKeySecretBytes      = 4096
	maxBaseURLBytes        = 4096
	keyProtectionDPAPI     = "dpapi-user"
	keyProtectionPlaintext = "plaintext"
	keyEnvelopeVersion     = 1
	keysLockRetry          = 50 * time.Millisecond
	keysLockWait           = 2 * time.Second
	keysReadRetry          = 20 * time.Millisecond
	keysRenameRetries      = 10
	keysRenameRetry        = 20 * time.Millisecond
	redactedText           = "[redacted]"
)

var (
	ErrKeyNotFound     = errors.New("harness: key profile not found")
	ErrKeyNeedsReentry = errors.New("harness: key is unavailable; enter it again")
	ErrKeyNameExists   = errors.New("harness: a key profile with this name already exists")
	ErrKeysFormat      = errors.New("harness: keys file is invalid or uses an unsupported format")
	ErrKeysBusy        = errors.New("harness: keys file is busy; try again")
)

// Probe categories double as the recorded last_check results.
var keyCheckResults = map[string]bool{
	"ok": true, "not_models": true, "redirect": true, "auth": true, "not_found": true,
	"rate_limited": true, "server": true, "unexpected": true, "timeout": true, "tls": true, "network": true,
}

// KeySecret is the stored key object. Data is DPAPI ciphertext (base64) on
// Windows and a protected envelope on other systems; the envelope binds the
// secret to the profile ID, normalized origin and protocol.
type KeySecret struct {
	Source        string `json:"source"`
	EnvName       string `json:"env_name,omitempty"`
	Protection    string `json:"protection,omitempty"`
	Data          string `json:"data,omitempty"`
	Hint          string `json:"hint,omitempty"`
	BoundOrigin   string `json:"bound_origin,omitempty"`
	BoundProtocol string `json:"bound_protocol,omitempty"`
	BoundProfile  string `json:"bound_profile,omitempty"`
}

func (KeySecret) String() string                 { return redactedText }
func (KeySecret) GoString() string               { return redactedText }
func (KeySecret) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, redactedText) }

type KeyCheck struct {
	At     time.Time `json:"at"`
	Result string    `json:"result"`
}

type KeyProfile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	Protocol  string    `json:"protocol"`
	Note      string    `json:"note,omitempty"`
	Key       KeySecret `json:"key"`
	UpdatedAt time.Time `json:"updated_at"`
	LastCheck *KeyCheck `json:"last_check,omitempty"`
}

// KeyProfileView is the only shape that CLI JSON and HTTP responses may use.
type KeyProfileView struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	BaseURL    string    `json:"base_url"`
	Protocol   string    `json:"protocol"`
	Note       string    `json:"note,omitempty"`
	Source     string    `json:"source"`
	EnvName    string    `json:"env_name,omitempty"`
	Protection string    `json:"protection,omitempty"`
	Encrypted  bool      `json:"encrypted"`
	Hint       string    `json:"hint,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastCheck  *KeyCheck `json:"last_check,omitempty"`
}

func (profile KeyProfile) View() KeyProfileView {
	view := KeyProfileView{
		ID: profile.ID, Name: profile.Name, BaseURL: profile.BaseURL, Protocol: normalizeKeyProtocol(profile.Protocol), Note: profile.Note,
		Source: profile.Key.Source, EnvName: profile.Key.EnvName, Protection: profile.Key.Protection,
		Encrypted: profile.Key.Source == KeySourceSaved && profile.Key.Protection == keyProtectionDPAPI,
		Hint:      profile.Key.Hint, UpdatedAt: profile.UpdatedAt,
	}
	if profile.LastCheck != nil {
		check := *profile.LastCheck
		view.LastCheck = &check
	}
	return view
}

// KeyInput carries user input for Add and Update. Secret is cleared after use.
// On Update, an empty Source keeps the current source; an empty Secret keeps
// the current saved key.
type KeyInput struct {
	Name, BaseURL, Note string
	Protocol            string
	Source              string
	EnvName             string
	Secret              []byte
}

func (KeyInput) String() string                 { return redactedText }
func (KeyInput) GoString() string               { return redactedText }
func (KeyInput) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, redactedText) }
func (KeyInput) MarshalJSON() ([]byte, error)   { return json.Marshal(redactedText) }

// ResolvedKey is a decrypted key for exactly one dispatch or probe.
type ResolvedKey struct {
	ProfileID string
	BaseURL   string
	Protocol  string
	secret    string
}

type keyEnvelope struct {
	Version   int    `json:"version"`
	ProfileID string `json:"profile_id"`
	BaseURL   string `json:"base_url"`
	Protocol  string `json:"protocol"`
	Secret    string `json:"secret"`
}

func (ResolvedKey) String() string                 { return redactedText }
func (ResolvedKey) GoString() string               { return redactedText }
func (ResolvedKey) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, redactedText) }
func (ResolvedKey) MarshalJSON() ([]byte, error)   { return json.Marshal(redactedText) }

// NewResolvedKey tests unsaved input. An empty secret is allowed only for
// loopback addresses.
func NewResolvedKey(baseURL, secret string) (ResolvedKey, error) {
	return NewResolvedKeyForProtocol(baseURL, secret, KeyProtocolOpenAICompatible)
}

func NewResolvedKeyForProtocol(baseURL, secret, protocol string) (ResolvedKey, error) {
	protocol = normalizeKeyProtocol(protocol)
	if !validKeyProtocol(protocol) {
		return ResolvedKey{}, errors.New("harness: unsupported key protocol")
	}
	base, err := NormalizeBaseURLForProtocol(baseURL, protocol)
	if err != nil {
		return ResolvedKey{}, err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		if !loopbackBaseURL(base) {
			return ResolvedKey{}, errors.New("harness: a key is required for non-loopback addresses")
		}
		return ResolvedKey{BaseURL: base, Protocol: protocol}, nil
	}
	if !validSecret(secret) {
		return ResolvedKey{}, errInvalidSecret
	}
	return ResolvedKey{BaseURL: base, Protocol: protocol, secret: secret}, nil
}

// WithBaseURL keeps the resolved secret private while allowing a form test to
// probe a changed address with the already resolved key.
func (key ResolvedKey) WithBaseURL(baseURL string) (ResolvedKey, error) {
	base, err := NormalizeBaseURLForProtocol(baseURL, key.Protocol)
	if err != nil {
		return ResolvedKey{}, err
	}
	key.BaseURL = base
	return key, nil
}

// WithProtocol keeps the resolved secret private while allowing a form test
// to validate a protocol change before the profile is saved.
func (key ResolvedKey) WithProtocol(protocol string) (ResolvedKey, error) {
	protocol = normalizeKeyProtocol(protocol)
	if !validKeyProtocol(protocol) {
		return ResolvedKey{}, errors.New("harness: unsupported key protocol")
	}
	base, err := NormalizeBaseURLForProtocol(key.BaseURL, protocol)
	if err != nil {
		return ResolvedKey{}, err
	}
	key.Protocol, key.BaseURL = protocol, base
	return key, nil
}

type KeyPreset struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	BaseURL      string            `json:"base_url"`
	Protocol     string            `json:"protocol"`
	Model        string            `json:"model"`
	Provider     string            `json:"provider"`
	Capabilities ModelCapabilities `json:"capabilities"`
}

// SuggestedModelCapabilities contains only stable local knowledge about a
// provider's named model. It does not probe the network or certify the model.
// DeepSeek's Responses web search is opt-in through this explicit model
// selection; other models retain the ordinary Chat Completions defaults.
func SuggestedModelCapabilities(key KeyProfileView, model string) ModelCapabilities {
	if key.Protocol != KeyProtocolOpenAICompatible || !strings.EqualFold(strings.TrimSpace(model), "deepseek-v4-pro") {
		return ModelCapabilities{}
	}
	parsed, err := url.Parse(key.BaseURL)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "api.deepseek.com") {
		return ModelCapabilities{}
	}
	return ModelCapabilities{Protocol: "responses", WebSearch: "supported"}
}

func KeyPresets() []KeyPreset {
	return []KeyPreset{
		{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Protocol: KeyProtocolOpenAICompatible, Provider: "openai-compatible", Model: "gpt-5"},
		{ID: "anthropic", Name: "Anthropic", BaseURL: "https://api.anthropic.com", Protocol: KeyProtocolAnthropic, Provider: "anthropic", Model: "claude-sonnet-4-6"},
		{ID: "gemini", Name: "Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Protocol: KeyProtocolOpenAICompatible, Provider: "openai-compatible", Model: "gemini-2.5-flash"},
		{ID: "xai", Name: "xAI", BaseURL: "https://api.x.ai/v1", Protocol: KeyProtocolOpenAICompatible, Provider: "openai-compatible", Model: "grok-4"},
		{ID: "glm", Name: "GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Protocol: KeyProtocolOpenAICompatible, Provider: "openai-compatible", Model: "glm-4.5"},
		{ID: "kimi", Name: "Kimi", BaseURL: "https://api.moonshot.cn/v1", Protocol: KeyProtocolOpenAICompatible, Provider: "openai-compatible", Model: "kimi-k2.5"},
		{ID: "minimax", Name: "MiniMax", BaseURL: "https://api.minimaxi.com/anthropic", Protocol: KeyProtocolAnthropic, Provider: "anthropic", Model: "MiniMax-M2.5"},
		{ID: "muse", Name: "Muse", BaseURL: "https://api.meta.ai/v1", Protocol: KeyProtocolOpenAICompatible, Provider: "openai-compatible", Model: "muse-spark-1.3"},
		{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Protocol: KeyProtocolOpenAICompatible, Provider: "openai-compatible", Model: "deepseek-flash"},
	}
}

var errInvalidSecret = errors.New("harness: key must be 1 to 4096 visible ASCII characters")

// NormalizeBaseURL accepts a service root, a /v1 address, or a full
// /chat/completions or /responses address and returns the base used for the
// provider endpoints. Only loopback hosts may use http.
func NormalizeBaseURL(raw string) (string, error) {
	return NormalizeBaseURLForProtocol(raw, KeyProtocolOpenAICompatible)
}

func NormalizeBaseURLForProtocol(raw, protocol string) (string, error) {
	protocol = normalizeKeyProtocol(protocol)
	if !validKeyProtocol(protocol) {
		return "", errors.New("harness: unsupported key protocol")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxBaseURLBytes {
		return "", errors.New("harness: base URL is required and must be at most 4096 bytes")
	}
	if strings.ContainsAny(raw, "?#") {
		return "", errors.New("harness: base URL must not contain a query or fragment")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("harness: base URL is not a valid address")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" || parsed.Opaque != "" || parsed.Hostname() == "" {
		return "", errors.New("harness: base URL must be an http or https address with a host")
	}
	if parsed.User != nil {
		return "", errors.New("harness: base URL must not contain user information")
	}
	if scheme == "http" && !loopbackHost(parsed.Hostname()) {
		return "", errors.New("harness: non-loopback base URLs must use https")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Host), ":")
	path := parsed.EscapedPath()
	for {
		path = strings.TrimRight(path, "/")
		trimmed := path
		switch protocol {
		case KeyProtocolAnthropic:
			for _, suffix := range []string{"/v1/messages", "/messages", "/v1"} {
				if candidate := strings.TrimSuffix(path, suffix); candidate != path {
					trimmed = candidate
					break
				}
			}
		default:
			trimmed = strings.TrimSuffix(path, "/chat/completions")
			if trimmed == path {
				trimmed = strings.TrimSuffix(path, "/responses")
			}
		}
		if trimmed == path {
			break
		}
		path = trimmed
	}
	base := scheme + "://" + host + path
	if len(base) > maxBaseURLBytes {
		return "", errors.New("harness: base URL is required and must be at most 4096 bytes")
	}
	return base, nil
}

func normalizeKeyProtocol(protocol string) string {
	if strings.TrimSpace(protocol) == "" {
		return KeyProtocolOpenAICompatible
	}
	return strings.TrimSpace(protocol)
}

func validKeyProtocol(protocol string) bool {
	protocol = normalizeKeyProtocol(protocol)
	return protocol == KeyProtocolOpenAICompatible || protocol == KeyProtocolAnthropic
}

func loopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func loopbackBaseURL(base string) bool {
	parsed, err := url.Parse(base)
	return err == nil && loopbackHost(parsed.Hostname())
}

func validKeyID(id string) bool {
	if len(id) != 10 || !strings.HasPrefix(id, "k-") {
		return false
	}
	for _, char := range id[2:] {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validSecret[T string | []byte](secret T) bool {
	if len(secret) == 0 || len(secret) > maxKeySecretBytes {
		return false
	}
	for index := 0; index < len(secret); index++ {
		if secret[index] < 0x21 || secret[index] > 0x7e {
			return false
		}
	}
	return true
}

func keyHint(secret []byte) string {
	if len(secret) < 12 {
		return ""
	}
	return string(secret[len(secret)-4:])
}

func hasControl(text string) bool { return strings.IndexFunc(text, unicode.IsControl) >= 0 }

func normalizeKeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	count := utf8.RuneCountInString(name)
	if count < 1 || count > 64 || !utf8.ValidString(name) || hasControl(name) {
		return "", errors.New("harness: key name must be 1 to 64 characters without control characters")
	}
	if validKeyID(strings.ToLower(name)) {
		return "", errors.New("harness: key name must not look like a key id")
	}
	return name, nil
}

func normalizeKeyNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 200 || !utf8.ValidString(note) || hasControl(note) {
		return "", errors.New("harness: key note must be at most 200 characters without control characters")
	}
	return note, nil
}

func validateStoredKey(key KeySecret, base string) error {
	switch key.Source {
	case KeySourceSaved:
		if key.Protection != keyProtectionDPAPI && key.Protection != keyProtectionPlaintext ||
			key.Data == "" || key.EnvName != "" || key.Hint != "" && (len(key.Hint) != 4 || !validSecret(key.Hint)) {
			return errors.New("saved key needs a known protection, data, and an optional 4-character hint")
		}
	case KeySourceEnv:
		if !validEnvName(key.EnvName) || key.Protection != "" || key.Data != "" || key.Hint != "" {
			return errors.New("env key needs only a valid variable name")
		}
	case KeySourceNone:
		if key.EnvName != "" || key.Protection != "" || key.Data != "" || key.Hint != "" {
			return errors.New("key source none stores no key material")
		}
		if !loopbackBaseURL(base) {
			return errors.New("key source none requires a loopback address")
		}
	default:
		return errors.New("key source must be saved, env, or none")
	}
	return nil
}

func validateProfiles(profiles []KeyProfile) error {
	ids := map[string]bool{}
	for index, profile := range profiles {
		problem := ""
		protocol := normalizeKeyProtocol(profile.Protocol)
		name, nameErr := normalizeKeyName(profile.Name)
		note, noteErr := normalizeKeyNote(profile.Note)
		base, baseErr := NormalizeBaseURLForProtocol(profile.BaseURL, protocol)
		switch {
		case !validKeyID(profile.ID) || ids[profile.ID]:
			problem = "id must be a unique k- id"
		case nameErr != nil || name != profile.Name:
			problem = "name is invalid"
		case baseErr != nil || base != profile.BaseURL:
			problem = "base_url is not normalized"
		case noteErr != nil || note != profile.Note:
			problem = "note is invalid"
		case profile.UpdatedAt.IsZero():
			problem = "updated_at is required"
		case profile.LastCheck != nil && (profile.LastCheck.At.IsZero() || !keyCheckResults[profile.LastCheck.Result]):
			problem = "last_check is invalid"
		}
		if problem == "" {
			if err := validateStoredKey(profile.Key, profile.BaseURL); err != nil {
				problem = err.Error()
			}
		}
		for _, earlier := range profiles[:index] {
			if problem == "" && strings.EqualFold(earlier.Name, profile.Name) {
				problem = "name is not unique"
			}
		}
		if problem != "" {
			return fmt.Errorf("%w: profile %d: %s", ErrKeysFormat, index+1, problem)
		}
		ids[profile.ID] = true
	}
	return nil
}

type keysFile struct {
	Version  int          `json:"version"`
	Profiles []KeyProfile `json:"profiles"`
}

// Decoder errors are not echoed: they could quote fragments of key material.
func decodeKeysFile(raw []byte) ([]KeyProfile, error) {
	var version struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(raw, &version) != nil {
		return nil, fmt.Errorf("%w: not a JSON object", ErrKeysFormat)
	}
	if version.Version != keysFileVersion {
		return nil, fmt.Errorf("%w: version %d is not supported", ErrKeysFormat, version.Version)
	}
	var file keysFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&file) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("%w: unknown fields or trailing content", ErrKeysFormat)
	}
	if err := validateProfiles(file.Profiles); err != nil {
		return nil, err
	}
	return file.Profiles, nil
}

func cloneProfiles(profiles []KeyProfile) []KeyProfile {
	cloned := slices.Clone(profiles)
	for index := range cloned {
		if cloned[index].LastCheck != nil {
			check := *cloned[index].LastCheck
			cloned[index].LastCheck = &check
		}
	}
	return cloned
}

func findProfile(profiles []KeyProfile, ref string) int {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return -1
	}
	for index := range profiles {
		if strings.EqualFold(profiles[index].ID, ref) {
			return index
		}
	}
	for index := range profiles {
		if strings.EqualFold(profiles[index].Name, ref) {
			return index
		}
	}
	return -1
}

func nameTaken(profiles []KeyProfile, name, exceptID string) bool {
	for _, profile := range profiles {
		if profile.ID != exceptID && strings.EqualFold(profile.Name, name) {
			return true
		}
	}
	return false
}

func newKeyID(profiles []KeyProfile) (string, error) {
	for {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		id := "k-" + hex.EncodeToString(raw[:])
		if !slices.ContainsFunc(profiles, func(profile KeyProfile) bool { return profile.ID == id }) {
			return id, nil
		}
	}
}

func keyTime() time.Time { return time.Now().UTC().Truncate(time.Second) }

// KeyStore reads keys.json lazily and caches it by modification time and size.
// Writers serialize in-process with mu and across processes with keys.json.lock.
type KeyStore struct {
	path  string
	mu    sync.Mutex
	cache keysCache
}

type keysCache struct {
	valid    bool
	modTime  time.Time
	size     int64
	profiles []KeyProfile
}

// NewKeyStore does not touch the filesystem.
func NewKeyStore(path string) *KeyStore { return &KeyStore{path: filepath.Clean(path)} }

// KeysEncrypted reports whether saved keys are encrypted on this system
// (DPAPI on Windows). Elsewhere they are stored as plain text.
func KeysEncrypted() bool { return keyProtection == keyProtectionDPAPI }

func (store *KeyStore) Path() string { return store.path }

func (store *KeyStore) List() ([]KeyProfileView, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	profiles, err := store.load()
	if err != nil {
		return nil, err
	}
	views := make([]KeyProfileView, 0, len(profiles))
	for _, profile := range profiles {
		views = append(views, profile.View())
	}
	return views, nil
}

// Find matches an id first and then a case-insensitive name. Names can never
// look like ids, so the two never collide.
func (store *KeyStore) Find(ref string) (KeyProfileView, error) {
	profile, err := store.profile(ref)
	if err != nil {
		return KeyProfileView{}, err
	}
	return profile.View(), nil
}

func (store *KeyStore) profile(ref string) (KeyProfile, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	profiles, err := store.load()
	if err != nil {
		return KeyProfile{}, err
	}
	index := findProfile(profiles, ref)
	if index < 0 {
		return KeyProfile{}, ErrKeyNotFound
	}
	return profiles[index], nil
}

func (store *KeyStore) Add(input KeyInput) (KeyProfileView, error) {
	defer clear(input.Secret)
	name, base, note, err := keyFields(input)
	if err != nil {
		return KeyProfileView{}, err
	}
	if input.Source == "" {
		return KeyProfileView{}, errors.New("harness: key source must be saved, env, or none")
	}
	profile := KeyProfile{Name: name, BaseURL: base, Protocol: normalizeKeyProtocol(input.Protocol), Note: note}
	store.mu.Lock()
	defer store.mu.Unlock()
	err = store.modify(func(profiles []KeyProfile) ([]KeyProfile, error) {
		if nameTaken(profiles, name, "") {
			return nil, ErrKeyNameExists
		}
		id, err := newKeyID(profiles)
		if err != nil {
			return nil, err
		}
		key, err := prepareKey(input, base, id)
		if err != nil {
			return nil, err
		}
		if key == nil {
			return nil, errors.New("harness: enter the key to save")
		}
		profile.ID, profile.UpdatedAt, profile.Key = id, keyTime(), *key
		profile.Key.BoundOrigin, profile.Key.BoundProtocol, profile.Key.BoundProfile = base, profile.Protocol, id
		return append(profiles, profile), nil
	})
	if err != nil {
		return KeyProfileView{}, err
	}
	return profile.View(), nil
}

// Update replaces name, address, and note. The key changes only when Source or
// Secret says so; a changed address or key clears the last connection check.
func (store *KeyStore) Update(ref string, input KeyInput) (KeyProfileView, error) {
	defer clear(input.Secret)
	name, err := normalizeKeyName(input.Name)
	if err != nil {
		return KeyProfileView{}, err
	}
	note, err := normalizeKeyNote(input.Note)
	if err != nil {
		return KeyProfileView{}, err
	}
	if input.Source == "" && input.EnvName != "" {
		return KeyProfileView{}, errors.New("harness: choose source env to change the environment variable")
	}
	var updated KeyProfile
	store.mu.Lock()
	defer store.mu.Unlock()
	err = store.modify(func(profiles []KeyProfile) ([]KeyProfile, error) {
		index := findProfile(profiles, ref)
		if index < 0 {
			return nil, ErrKeyNotFound
		}
		current := profiles[index]
		protocol := normalizeKeyProtocol(current.Protocol)
		if input.Protocol != "" {
			protocol = normalizeKeyProtocol(input.Protocol)
		}
		if !validKeyProtocol(protocol) {
			return nil, errors.New("harness: key protocol must be openai-compatible or anthropic")
		}
		base, err := NormalizeBaseURLForProtocol(input.BaseURL, protocol)
		if err != nil {
			return nil, err
		}
		if nameTaken(profiles, name, current.ID) {
			return nil, ErrKeyNameExists
		}
		keyInput := input
		if keyInput.Protocol == "" {
			keyInput.Protocol = protocol
		}
		replacement, err := prepareKey(keyInput, base, current.ID)
		if err != nil {
			return nil, err
		}
		key := current.Key
		switch {
		case replacement != nil && (input.Source != "" || current.Key.Source == KeySourceSaved):
			key = *replacement
		case replacement != nil:
			return nil, errors.New("harness: this profile does not store a key; choose source saved")
		case input.Source != "" && input.Source != current.Key.Source:
			return nil, errors.New("harness: enter the key to save")
		}
		if key.Source == KeySourceNone && !loopbackBaseURL(base) {
			return nil, errors.New("harness: a profile without a key must use a loopback address")
		}
		if replacement != nil || key.Source != KeySourceSaved {
			key.BoundOrigin, key.BoundProtocol, key.BoundProfile = base, protocol, current.ID
		}
		next := current
		next.Name, next.BaseURL, next.Protocol, next.Note, next.Key, next.UpdatedAt = name, base, protocol, note, key, keyTime()
		if next.BaseURL != current.BaseURL || next.Protocol != current.Protocol || next.Key != current.Key {
			next.LastCheck = nil
		}
		profiles[index], updated = next, next
		return profiles, nil
	})
	if err != nil {
		return KeyProfileView{}, err
	}
	return updated.View(), nil
}

// Models that reference a removed profile become missing; they are not edited.
func (store *KeyStore) Remove(ref string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.modify(func(profiles []KeyProfile) ([]KeyProfile, error) {
		index := findProfile(profiles, ref)
		if index < 0 {
			return nil, ErrKeyNotFound
		}
		return slices.Delete(profiles, index, index+1), nil
	})
}

// RecordCheck stores a connection test result without changing updated_at.
func (store *KeyStore) RecordCheck(ref, result string) error {
	if !keyCheckResults[result] {
		return errors.New("harness: unknown key check result")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.modify(func(profiles []KeyProfile) ([]KeyProfile, error) {
		index := findProfile(profiles, ref)
		if index < 0 {
			return nil, ErrKeyNotFound
		}
		profiles[index].LastCheck = &KeyCheck{At: keyTime(), Result: result}
		return profiles, nil
	})
}

// Resolve decrypts or reads the key immediately before one dispatch or probe.
func (store *KeyStore) Resolve(ref string) (ResolvedKey, error) {
	profile, err := store.profile(ref)
	if err != nil {
		return ResolvedKey{}, err
	}
	resolved := ResolvedKey{ProfileID: profile.ID, BaseURL: profile.BaseURL, Protocol: normalizeKeyProtocol(profile.Protocol)}
	switch profile.Key.Source {
	case KeySourceNone:
		return resolved, nil
	case KeySourceEnv:
		value := strings.TrimSpace(os.Getenv(profile.Key.EnvName))
		if !validSecret(value) {
			return ResolvedKey{}, fmt.Errorf("%w (profile %q reads environment variable %s)", ErrKeyNeedsReentry, profile.Name, profile.Key.EnvName)
		}
		resolved.secret = value
		return resolved, nil
	default:
		plain, err := unprotectKey(profile.Key.Protection, profile.Key.Data)
		defer clear(plain)
		if err != nil {
			return ResolvedKey{}, fmt.Errorf("%w (profile %q)", ErrKeyNeedsReentry, profile.Name)
		}
		secret, envelopeErr := openKeyEnvelope(plain, profile.ID, profile.BaseURL, resolved.Protocol)
		if envelopeErr != nil || profile.Key.BoundOrigin != profile.BaseURL || profile.Key.BoundProtocol != resolved.Protocol || profile.Key.BoundProfile != profile.ID {
			return ResolvedKey{}, fmt.Errorf("%w (profile %q origin or protocol binding changed)", ErrKeyNeedsReentry, profile.Name)
		}
		resolved.secret = secret
		return resolved, nil
	}
}

func keyFields(input KeyInput) (name, base, note string, err error) {
	if name, err = normalizeKeyName(input.Name); err != nil {
		return "", "", "", err
	}
	if base, err = NormalizeBaseURLForProtocol(input.BaseURL, input.Protocol); err != nil {
		return "", "", "", err
	}
	if note, err = normalizeKeyNote(input.Note); err != nil {
		return "", "", "", err
	}
	return name, base, note, nil
}

// prepareKey returns nil when the input keeps the current key: an empty
// Source, or Source saved without a secret.
func prepareKey(input KeyInput, base, profileID string) (*KeySecret, error) {
	secret := bytes.TrimSpace(input.Secret)
	switch input.Source {
	case "", KeySourceSaved:
		if input.EnvName != "" {
			return nil, errors.New("harness: only source env uses an environment variable")
		}
		if len(secret) == 0 {
			return nil, nil
		}
		if !validSecret(secret) {
			return nil, errInvalidSecret
		}
		data, err := sealKeyEnvelope(profileID, base, normalizeKeyProtocol(input.Protocol), secret)
		if err != nil {
			return nil, err
		}
		return &KeySecret{Source: KeySourceSaved, Protection: keyProtection, Data: data, Hint: keyHint(secret)}, nil
	case KeySourceEnv:
		if len(secret) != 0 {
			return nil, errors.New("harness: source env reads the key from the environment; do not enter it")
		}
		if !validEnvName(input.EnvName) {
			return nil, errors.New("harness: invalid environment variable name")
		}
		return &KeySecret{Source: KeySourceEnv, EnvName: input.EnvName}, nil
	case KeySourceNone:
		if len(secret) != 0 || input.EnvName != "" {
			return nil, errors.New("harness: source none uses no key")
		}
		if !loopbackBaseURL(base) {
			return nil, errors.New("harness: a profile without a key must use a loopback address")
		}
		return &KeySecret{Source: KeySourceNone}, nil
	default:
		return nil, errors.New("harness: key source must be saved, env, or none")
	}
}

func sealKeyEnvelope(profileID, baseURL, protocol string, secret []byte) (string, error) {
	envelope := keyEnvelope{Version: keyEnvelopeVersion, ProfileID: profileID, BaseURL: baseURL, Protocol: normalizeKeyProtocol(protocol), Secret: string(secret)}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	return protectKey(raw)
}

func openKeyEnvelope(raw []byte, profileID, baseURL, protocol string) (string, error) {
	var envelope keyEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != keyEnvelopeVersion ||
		envelope.ProfileID != profileID || envelope.BaseURL != baseURL || envelope.Protocol != normalizeKeyProtocol(protocol) || !validSecret(envelope.Secret) {
		return "", errors.New("harness: key binding does not match the current profile")
	}
	return envelope.Secret, nil
}

// load returns cached profiles while the file is unchanged. Callers hold mu.
func (store *KeyStore) load() ([]KeyProfile, error) {
	info, err := os.Lstat(store.path)
	if errors.Is(err, fs.ErrNotExist) {
		store.cache = keysCache{}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("harness: keys file: %w", err)
	}
	if store.cache.valid && info.ModTime().Equal(store.cache.modTime) && info.Size() == store.cache.size {
		return cloneProfiles(store.cache.profiles), nil
	}
	profiles, stat, err := store.read()
	if errors.Is(err, fs.ErrNotExist) {
		store.cache = keysCache{}
		return nil, nil
	}
	if err != nil {
		store.cache = keysCache{}
		return nil, err
	}
	store.cache = keysCache{valid: true, modTime: stat.ModTime(), size: stat.Size(), profiles: cloneProfiles(profiles)}
	return profiles, nil
}

// read retries once so a reader racing a replaced file sees the new version.
func (store *KeyStore) read() ([]KeyProfile, os.FileInfo, error) {
	profiles, info, err := readKeysFile(store.path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return profiles, info, err
	}
	time.Sleep(keysReadRetry)
	return readKeysFile(store.path)
}

func readKeysFile(path string) ([]KeyProfile, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: keys file must be a regular file", ErrKeysFormat)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("harness: open keys file: %w", err)
	}
	defer file.Close()
	if checkRegularHandle(file) != nil {
		return nil, nil, fmt.Errorf("%w: keys file must be a regular, unlinked file", ErrKeysFormat)
	}
	stat, err := file.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("harness: keys file: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxKeysFileBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("harness: read keys file: %w", err)
	}
	if len(raw) > maxKeysFileBytes {
		return nil, nil, fmt.Errorf("%w: file exceeds 1 MiB", ErrKeysFormat)
	}
	profiles, err := decodeKeysFile(raw)
	if err != nil {
		return nil, nil, err
	}
	return profiles, stat, nil
}

// modify edits a fresh copy of the file under keys.json.lock and publishes it
// with a same-directory rename. Callers hold mu.
func (store *KeyStore) modify(edit func([]KeyProfile) ([]KeyProfile, error)) error {
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("harness: create keys directory: %w", err)
	}
	lock, err := acquireKeysLock(store.path + ".lock")
	if err != nil {
		return err
	}
	defer lock.release()
	profiles, _, err := store.read()
	if errors.Is(err, fs.ErrNotExist) {
		profiles, err = nil, nil
	}
	if err != nil {
		return err
	}
	next, err := edit(profiles)
	if err != nil {
		return err
	}
	if next == nil {
		next = []KeyProfile{}
	}
	if err := validateProfiles(next); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(keysFile{Version: keysFileVersion, Profiles: next}, "", "  ")
	if err != nil {
		return fmt.Errorf("harness: encode keys file: %w", err)
	}
	raw = append(raw, '\n')
	if len(raw) > maxKeysFileBytes {
		return errors.New("harness: keys file would exceed 1 MiB")
	}
	store.cache = keysCache{}
	if err := publishKeysFile(directory, store.path, raw); err != nil {
		return err
	}
	if info, err := os.Lstat(store.path); err == nil {
		store.cache = keysCache{valid: true, modTime: info.ModTime(), size: info.Size(), profiles: cloneProfiles(next)}
	}
	return nil
}

func acquireKeysLock(path string) (processFileLock, error) {
	deadline := time.Now().Add(keysLockWait)
	for {
		lock, err := acquireProcessFileLock(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, ErrOwnerActive) {
			return processFileLock{}, fmt.Errorf("harness: keys lock: %w", err)
		}
		if time.Now().After(deadline) {
			return processFileLock{}, ErrKeysBusy
		}
		time.Sleep(keysLockRetry)
	}
}

func publishKeysFile(directory, target string, raw []byte) error {
	temp, err := os.CreateTemp(directory, ".keys-*.tmp")
	if err != nil {
		return fmt.Errorf("harness: create keys staging file: %w", err)
	}
	tempPath := temp.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("harness: secure keys staging file: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return fmt.Errorf("harness: write keys file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("harness: sync keys file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("harness: close keys file: %w", err)
	}
	// A reader on Windows can briefly block the replacing rename.
	for attempt := 0; ; attempt++ {
		err = os.Rename(tempPath, target)
		if err == nil {
			break
		}
		if attempt == keysRenameRetries {
			return fmt.Errorf("harness: publish keys file: %w", err)
		}
		time.Sleep(keysRenameRetry)
	}
	published = true
	return nil
}
