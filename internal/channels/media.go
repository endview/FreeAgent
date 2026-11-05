package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/endview/freeagent/internal/channelwire"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// MediaRef comes only from an authenticated platform event, never a model URL.
// Transport secrets and temporary download URLs do not enter message metadata.
type MediaRef = channelwire.MediaRef

func multipartMedia(field, name string, raw []byte, fields map[string]string) ([]byte, string, error) {
	if len(raw) > 32<<20 || name == "" || len(name) > 512 || strings.ContainsAny(name, "\x00\r\n/\\") {
		return nil, "", errors.New("channel: invalid bounded file upload")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", err
		}
	}
	file, err := writer.CreateFormFile(field, name)
	if err != nil {
		return nil, "", err
	}
	if _, err := file.Write(raw); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}
func (api *TelegramAPI) DownloadMedia(ctx context.Context, ref MediaRef, limit int64) ([]byte, error) {
	if !validID(ref.ID) || limit < 1 || limit > 16<<20 || ref.Bytes > limit {
		return nil, errors.New("telegram: file limit or identity invalid")
	}
	var result struct {
		ID   string `json:"file_id"`
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := api.call(ctx, "getFile", map[string]string{"file_id": ref.ID}, &result, false); err != nil {
		return nil, err
	}
	if result.ID != ref.ID || result.Size > limit || result.Path == "" || path.Clean(result.Path) != result.Path || strings.HasPrefix(result.Path, "/") || strings.Contains(result.Path, "..") || strings.ContainsAny(result.Path, "\\?#%\x00\r\n") {
		return nil, errors.New("telegram: invalid file response")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api.base+"/file/bot"+api.token+"/"+result.Path, nil)
	if err != nil {
		return nil, &TelegramError{}
	}
	response, err := api.http.Do(req)
	if err != nil {
		return nil, &TelegramError{}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.ContentLength > limit {
		return nil, &TelegramError{Status: response.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("telegram: file download interrupted or oversized")
	}
	return raw, nil
}
func (api *TelegramAPI) SendDocument(ctx context.Context, chat, reply int64, name string, raw []byte) (int64, error) {
	if chat == 0 {
		return 0, errors.New("telegram: invalid file target")
	}
	fields := map[string]string{"chat_id": strconv.FormatInt(chat, 10)}
	if reply > 0 {
		r, _ := json.Marshal(map[string]int64{"message_id": reply})
		fields["reply_parameters"] = string(r)
	}
	body, kind, err := multipartMedia("document", name, raw, fields)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api.base+"/bot"+api.token+"/sendDocument", bytes.NewReader(body))
	if err != nil {
		return 0, &TelegramError{}
	}
	req.Header.Set("Content-Type", kind)
	response, err := api.http.Do(req)
	if err != nil {
		return 0, &TelegramError{Uncertain: true}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, telegramBodyLimit+1))
	if err != nil || len(data) > telegramBodyLimit {
		return 0, &TelegramError{Status: response.StatusCode, Uncertain: true}
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result TelegramMessage `json:"result"`
		Code   int             `json:"error_code"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return 0, &TelegramError{Status: response.StatusCode, Uncertain: true}
	}
	if response.StatusCode != 200 || !envelope.OK {
		return 0, &TelegramError{Status: response.StatusCode, Uncertain: response.StatusCode >= 500}
	}
	if envelope.Result.ID <= 0 || envelope.Result.Chat.ID != chat {
		return 0, &TelegramError{Status: response.StatusCode, Uncertain: true}
	}
	return envelope.Result.ID, nil
}
func (a *LarkAPI) DownloadMedia(ctx context.Context, message string, ref MediaRef, limit int64) ([]byte, error) {
	if !validID(message) || !validID(ref.ID) || limit < 1 || limit > 16<<20 || ref.Bytes > limit {
		return nil, errors.New("lark: invalid bounded resource")
	}
	token, err := a.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	kind := "file"
	if ref.Image {
		kind = "image"
	}
	endpoint := "/open-apis/im/v1/messages/" + url.PathEscape(message) + "/resources/" + url.PathEscape(ref.ID) + "?type=" + kind
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.api.base+endpoint, nil)
	if err != nil {
		return nil, &APIError{Platform: "lark"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := a.api.http.Do(req)
	if err != nil {
		return nil, &APIError{Platform: "lark"}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.ContentLength > limit {
		return nil, &APIError{Platform: "lark", Status: response.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("lark: resource interrupted or oversized")
	}
	return raw, nil
}
func (a *LarkAPI) SendDocument(ctx context.Context, chat, reply, name string, raw []byte) (string, error) {
	if !validID(chat) || reply != "" && !validID(reply) {
		return "", errors.New("lark: invalid file target")
	}
	token, err := a.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	body, kind, err := multipartMedia("file", name, raw, map[string]string{"file_type": "stream", "file_name": name})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.api.base+"/open-apis/im/v1/files", bytes.NewReader(body))
	if err != nil {
		return "", &APIError{Platform: "lark"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", kind)
	response, err := a.api.http.Do(req)
	if err != nil {
		return "", &APIError{Platform: "lark", Uncertain: true}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Key string `json:"file_key"`
		} `json:"data"`
	}
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &envelope) != nil {
		return "", &APIError{Platform: "lark", Status: response.StatusCode, Uncertain: true}
	}
	if response.StatusCode != 200 || envelope.Code != 0 || !validID(envelope.Data.Key) {
		return "", &APIError{Platform: "lark", Status: response.StatusCode, Code: envelope.Code, Uncertain: response.StatusCode >= 500}
	}
	content, _ := json.Marshal(map[string]string{"file_key": envelope.Data.Key})
	input := map[string]string{"receive_id": chat, "msg_type": "file", "content": string(content)}
	endpoint := "/open-apis/im/v1/messages?receive_id_type=chat_id"
	if reply != "" {
		endpoint = "/open-apis/im/v1/messages/" + url.PathEscape(reply) + "/reply"
		delete(input, "receive_id")
	}
	var sent struct {
		Data struct {
			Message string `json:"message_id"`
		} `json:"data"`
	}
	if err := a.api.request(ctx, endpoint, "Bearer "+token, input, &sent, true); err != nil {
		return "", err
	}
	if !validID(sent.Data.Message) {
		return "", &APIError{Platform: "lark", Status: 200, Uncertain: true}
	}
	return sent.Data.Message, nil
}
