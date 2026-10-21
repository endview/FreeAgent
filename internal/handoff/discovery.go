package handoff

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	EngineFile     = "engine.json"
	SecretFile     = "attach.secret"
	ChatFile       = "chat.secret"
	Protocol       = "freeagent-attach-v1"
	DataFormat     = "freeagent-harness-v7"
	Product        = "FreeAgent"
	MaxEngineBytes = 64 << 10
)

type EngineInfo struct {
	Protocol    string `json:"protocol"`
	Product     string `json:"product"`
	DataFormat  string `json:"data_format"`
	Origin      string `json:"origin"`
	Workspace   string `json:"workspace"`
	Generation  string `json:"generation"`
	PID         int    `json:"pid"`
	OwnerKind   string `json:"owner_kind"`
	PublishedAt string `json:"published_at"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

type Published struct {
	Info       EngineInfo
	Secret     string
	ChatSecret string
	dataDir    string
}

func NewSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}

func Publish(dataDir string, info EngineInfo, secret, chatSecret string) (Published, error) {
	if info.Product == "" {
		info.Product = Product
	}
	info.Protocol, info.DataFormat = Protocol, DataFormat
	if err := validateInfo(info); err != nil {
		return Published{}, err
	}
	if len(secret) < 32 || strings.ContainsAny(secret, "\x00\r\n") {
		return Published{}, errors.New("handoff: attach secret is invalid")
	}
	if len(chatSecret) < 32 || strings.ContainsAny(chatSecret, "\x00\r\n") {
		return Published{}, errors.New("handoff: chat secret is invalid")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Published{}, err
	}
	if info.PublishedAt == "" {
		info.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := atomicWrite(filepath.Join(dataDir, EngineFile), mustJSON(info), 0o600); err != nil {
		return Published{}, err
	}
	if err := atomicWrite(filepath.Join(dataDir, SecretFile), []byte(secret+"\n"), 0o600); err != nil {
		_ = os.Remove(filepath.Join(dataDir, EngineFile))
		return Published{}, err
	}
	if err := atomicWrite(filepath.Join(dataDir, ChatFile), []byte(chatSecret+"\n"), 0o600); err != nil {
		_ = os.Remove(filepath.Join(dataDir, EngineFile))
		_ = os.Remove(filepath.Join(dataDir, SecretFile))
		return Published{}, err
	}
	return Published{Info: info, Secret: secret, ChatSecret: chatSecret, dataDir: dataDir}, nil
}

func (published Published) Close() error {
	if published.dataDir == "" {
		return nil
	}
	return errors.Join(os.Remove(filepath.Join(published.dataDir, EngineFile)), os.Remove(filepath.Join(published.dataDir, SecretFile)), os.Remove(filepath.Join(published.dataDir, ChatFile)))
}

func Read(dataDir string) (EngineInfo, string, string, error) {
	engineRaw, err := os.ReadFile(filepath.Join(dataDir, EngineFile))
	if err != nil {
		return EngineInfo{}, "", "", err
	}
	if len(engineRaw) == 0 || len(engineRaw) > MaxEngineBytes {
		return EngineInfo{}, "", "", errors.New("handoff: engine discovery is too large")
	}
	var info EngineInfo
	if err := json.Unmarshal(engineRaw, &info); err != nil {
		return EngineInfo{}, "", "", err
	}
	if err := validateInfo(info); err != nil {
		return EngineInfo{}, "", "", err
	}
	if info.Product != Product || info.Protocol != Protocol || info.DataFormat != DataFormat {
		return EngineInfo{}, "", "", errors.New("handoff: incomplete owner identity")
	}
	secretRaw, err := os.ReadFile(filepath.Join(dataDir, SecretFile))
	if err != nil {
		return EngineInfo{}, "", "", err
	}
	secret := strings.TrimSpace(string(secretRaw))
	if len(secret) < 32 || strings.ContainsAny(secret, "\x00\r\n") {
		return EngineInfo{}, "", "", errors.New("handoff: attach secret is invalid")
	}
	chatRaw, err := os.ReadFile(filepath.Join(dataDir, ChatFile))
	if err != nil {
		return EngineInfo{}, "", "", err
	}
	chatSecret := strings.TrimSpace(string(chatRaw))
	if len(chatSecret) < 32 || strings.ContainsAny(chatSecret, "\x00\r\n") {
		return EngineInfo{}, "", "", errors.New("handoff: chat secret is invalid")
	}
	if info.ExpiresAt != "" {
		expires, parseErr := time.Parse(time.RFC3339Nano, info.ExpiresAt)
		if parseErr != nil || time.Now().UTC().After(expires) {
			return EngineInfo{}, "", "", errors.New("handoff: owner lease expired")
		}
	}
	return info, secret, chatSecret, nil
}

func UpdateWorkspace(dataDir, workspace string) error {
	path := filepath.Join(dataDir, EngineFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var info EngineInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return err
	}
	if err := validateInfo(info); err != nil {
		return err
	}
	info.Workspace = workspace
	return atomicWrite(path, mustJSON(info), 0o600)
}

func validateInfo(info EngineInfo) error {
	if info.Protocol != "" && info.Protocol != Protocol || info.DataFormat != "" && info.DataFormat != DataFormat {
		return errors.New("handoff: incompatible owner protocol or data format")
	}
	if info.Product != "" && info.Product != Product {
		return errors.New("handoff: incompatible owner product")
	}
	parsed, err := url.Parse(info.Origin)
	if err != nil || parsed.Scheme != "http" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || net.ParseIP(parsed.Hostname()) == nil || !net.ParseIP(parsed.Hostname()).IsLoopback() || parsed.Port() == "" {
		return errors.New("handoff: owner origin must be a numeric loopback HTTP origin")
	}
	if info.Generation == "" || len(info.Generation) > 128 || strings.ContainsAny(info.Generation, "\x00\r\n") {
		return errors.New("handoff: owner generation is invalid")
	}
	if info.OwnerKind != "interactive" && info.OwnerKind != "one_shot" {
		return errors.New("handoff: owner kind is invalid")
	}
	return nil
}

func atomicWrite(path string, raw []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".handoff-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return append(raw, '\n')
}
