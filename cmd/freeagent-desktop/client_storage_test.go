package main

import (
	"strings"
	"testing"
)

func TestClientStorageIsScopedAndRejectsSecretsAndTraversal(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("a", 64)
	for _, item := range []struct{ kind, raw string }{
		{"view", `{"theme":"system","sidebar":252,"reading":820,"composer":190}`},
		{"drafts", `{"messages":[["chat","中文草稿"]],"requests":[],"attachments":[]}`},
	} {
		if err := saveClientFile(root, id, item.kind, item.raw); err != nil {
			t.Fatal(err)
		}
		got, err := loadClientFile(root, id, item.kind)
		if err != nil || got != item.raw {
			t.Fatal("client record did not round trip", err)
		}
		if got, err := loadClientFile(root, strings.Repeat("b", 64), item.kind); err != nil || got != "" {
			t.Fatal("record crossed environment")
		}
	}
	for _, item := range []struct{ kind, raw string }{
		{"view", `{"theme":"dark","sidebar":252,"reading":820,"composer":190,"key":"secret"}`},
		{"view", `{"theme":"dark","sidebar":999999,"reading":820,"composer":190}`},
		{"drafts", `{"providers":{"secret":"key"}}`},
		{"drafts", `{"messages":[],"requests":[["chat",{"request_key":"a","prompt":"x","kind":"submit","api_key":"secret"}]],"attachments":[]}`},
	} {
		if err := saveClientFile(root, id, item.kind, item.raw); err == nil {
			t.Fatal("accepted unsupported client fields", item.kind)
		}
	}
	if err := saveClientFile(root, "../../escape", "view", `{}`); err == nil {
		t.Fatal("accepted traversal")
	}
}
