package channels

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramMediaOfficialPathsLimitsAndDeliveryCertainty(t *testing.T) {
	const token = "12345678:MEDIA_FIXTURE"
	filePath := "documents/file.txt"
	fileBody := "hello"
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			fmt.Fprintf(w, `{"ok":true,"result":{"file_id":"file_1","file_size":5,"file_path":%q}}`, filePath)
		case r.URL.Path == "/file/bot"+token+"/documents/file.txt":
			io.WriteString(w, fileBody)
		case strings.HasSuffix(r.URL.Path, "/sendDocument"):
			sent++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("document")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			raw, _ := io.ReadAll(file)
			if header.Filename != "中文 & !.md" || string(raw) != "hello" || r.FormValue("chat_id") != "42" {
				t.Errorf("multipart lost target/name/bytes")
			}
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":9,"chat":{"id":42}}}`)
		default:
			t.Errorf("unexpected media endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api, err := NewTelegramAPI(server.URL, token, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := api.DownloadMedia(t.Context(), MediaRef{ID: "file_1", Name: "file.txt", Bytes: 5}, 8)
	if err != nil || string(raw) != "hello" {
		t.Fatalf("download %q %v", raw, err)
	}
	fileBody = "exceeds-8"
	if _, err := api.DownloadMedia(t.Context(), MediaRef{ID: "file_1"}, 8); err == nil {
		t.Fatal("actual bytes not bounded")
	}
	filePath = "../secrets"
	if _, err := api.DownloadMedia(t.Context(), MediaRef{ID: "file_1"}, 8); err == nil || strings.Contains(err.Error(), token) {
		t.Fatal("unsafe path or error redaction", err)
	}
	if id, err := api.SendDocument(t.Context(), 42, 3, "中文 & !.md", []byte("hello")); err != nil || id != 9 || sent != 1 {
		t.Fatalf("send %d %v", id, err)
	}
}
func TestLarkMediaTokenResourceAndMultipartUpload(t *testing.T) {
	var phases []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		phases = append(phases, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "tenant_access_token/internal") {
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"fixture-token","expire":3600}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing app token")
		}
		switch r.URL.Path {
		case "/open-apis/im/v1/messages/msg_1/resources/file_1":
			if r.Method != "GET" || r.URL.Query().Get("type") != "file" {
				t.Error("resource binding lost")
			}
			io.WriteString(w, "file bytes")
		case "/open-apis/im/v1/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			raw, _ := io.ReadAll(file)
			if r.FormValue("file_type") != "stream" || header.Filename != "result.md" || string(raw) != "file bytes" {
				t.Error("file multipart mismatch")
			}
			fmt.Fprint(w, `{"code":0,"data":{"file_key":"uploaded_1"}}`)
		case "/open-apis/im/v1/messages/msg_1/reply":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["msg_type"] != "file" || !strings.Contains(fmt.Sprint(body["content"]), "uploaded_1") {
				t.Error(body)
			}
			fmt.Fprint(w, `{"code":0,"data":{"message_id":"sent_1"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api, err := NewLarkAPI(server.URL, "app_1", "fixture-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := api.DownloadMedia(t.Context(), "msg_1", MediaRef{ID: "file_1", Name: "file.txt"}, 1024)
	if err != nil || string(raw) != "file bytes" {
		t.Fatalf("download %q %v", raw, err)
	}
	id, err := api.SendDocument(t.Context(), "chat_1", "msg_1", "result.md", raw)
	if err != nil || id != "sent_1" || len(phases) != 4 {
		t.Fatalf("send %s %v %v", id, err, phases)
	}
}
