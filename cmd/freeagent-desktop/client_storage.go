package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Only explicitly allowed client fields are stored. This directory is private
// to this portable WebView profile; ports and Core execution config are not keys.
type clientView struct {
	Theme    string `json:"theme"`
	Sidebar  int    `json:"sidebar"`
	Reading  int    `json:"reading"`
	Composer int    `json:"composer"`
}

func clientEnvironment(data, workspace string) string {
	digest := sha256.Sum256([]byte(data + "\x00" + workspace))
	return hex.EncodeToString(digest[:])
}

type clientSend struct {
	RequestKey   string   `json:"request_key"`
	Prompt       string   `json:"prompt"`
	Attachments  []string `json:"attachments"`
	Kind         string   `json:"kind"`
	Permission   string   `json:"permission"`
	ConfirmFull  bool     `json:"confirm_full_access"`
	Capabilities struct {
		Skills     []string `json:"skills,omitempty"`
		MCP        []string `json:"mcp_servers,omitempty"`
		Extensions []string `json:"extensions,omitempty"`
	} `json:"capabilities"`
	Model string `json:"model"`
}
type clientDrafts struct {
	Messages    [][2]json.RawMessage `json:"messages"`
	Requests    [][2]json.RawMessage `json:"requests"`
	Attachments [][2]json.RawMessage `json:"attachments"`
}

func decodeClientJSON(raw string, value any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid client data")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid client data suffix")
	}
	return nil
}
func clientFile(root, environment, kind string) (string, error) {
	id, err := hex.DecodeString(environment)
	if err != nil || len(id) != 32 || environment != hex.EncodeToString(id) {
		return "", errors.New("invalid client environment")
	}
	if kind != "view" && kind != "drafts" {
		return "", errors.New("invalid client storage kind")
	}
	dir := filepath.Join(root, "freeagent-client", kind)
	return filepath.Join(dir, environment+".json"), nil
}
func loadClientFile(root, environment, kind string) (string, error) {
	path, err := clientFile(root, environment, kind)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return "", errors.New("invalid client file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
func saveClientFile(root, environment, kind, raw string) error {
	path, err := clientFile(root, environment, kind)
	if err != nil {
		return err
	}
	if len(raw) > 4<<20 {
		return errors.New("client data exceeds limit")
	}
	if kind == "view" {
		var view clientView
		if decodeClientJSON(raw, &view) != nil || (view.Theme != "light" && view.Theme != "dark" && view.Theme != "system") || view.Sidebar < 208 || view.Sidebar > 380 || view.Reading < 540 || view.Reading > 1120 || view.Composer < 88 || view.Composer > 380 {
			return errors.New("invalid client view")
		}
	} else {
		var drafts clientDrafts
		if decodeClientJSON(raw, &drafts) != nil || len(drafts.Messages) > 100 || len(drafts.Requests) > 100 || len(drafts.Attachments) > 100 {
			return errors.New("invalid client drafts")
		}
		for _, pair := range drafts.Messages {
			var id, text string
			if json.Unmarshal(pair[0], &id) != nil || json.Unmarshal(pair[1], &text) != nil || len(id) > 128 || len(text) > 1<<20 {
				return errors.New("invalid message draft")
			}
		}
		for _, pair := range drafts.Requests {
			var id string
			var send clientSend
			if json.Unmarshal(pair[0], &id) != nil || decodeClientJSON(string(pair[1]), &send) != nil || len(id) > 128 || len(send.RequestKey) > 128 || send.RequestKey == "" || len(send.Prompt) > 1<<20 || (send.Kind != "submit" && send.Kind != "queue") {
				return errors.New("invalid pending input")
			}
		}
		for _, pair := range drafts.Attachments {
			var id string
			var refs []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				MIME        string `json:"mime"`
				SHA256      string `json:"sha256"`
				Bytes       int64  `json:"bytes"`
				Source      string `json:"source,omitempty"`
				Width       int    `json:"width,omitempty"`
				Height      int    `json:"height,omitempty"`
				Public      bool   `json:"public,omitempty"`
				Unavailable bool   `json:"unavailable,omitempty"`
			}
			if json.Unmarshal(pair[0], &id) != nil || len(id) > 128 || decodeClientJSON(string(pair[1]), &refs) != nil || len(refs) > 8 {
				return errors.New("invalid attachment drafts")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".client-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.WriteString(raw); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
