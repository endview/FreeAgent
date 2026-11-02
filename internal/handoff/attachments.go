package handoff

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/endview/freeagent/internal/harness"
)

func (client *Client) UploadAttachment(ctx context.Context, chat, name string, reader io.Reader) (harness.AttachmentRef, error) {
	return client.UploadAttachmentRequest(ctx, chat, name, "", reader)
}
func (client *Client) UploadAttachmentRequest(ctx context.Context, chat, name, key string, reader io.Reader) (harness.AttachmentRef, error) {
	query := url.Values{"conversation_id": {chat}, "name": {name}, "request_key": {key}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.Origin+"/api/attachments?"+query.Encode(), reader)
	if err != nil {
		return harness.AttachmentRef{}, err
	}
	request.Header.Set(client.Header, client.Secret)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return harness.AttachmentRef{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return harness.AttachmentRef{}, &APIError{Status: response.StatusCode, Code: "attachment_upload"}
	}
	var ref harness.AttachmentRef
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&ref)
	return ref, err
}
func (client *Client) Attachments(ctx context.Context, chat string) ([]harness.AttachmentInfo, error) {
	var result []harness.AttachmentInfo
	err := client.request(ctx, http.MethodGet, "/api/attachments?conversation_id="+url.QueryEscape(chat), nil, &result)
	return result, err
}
func (client *Client) DownloadAttachment(ctx context.Context, chat, id string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.Origin+"/api/attachments/"+url.PathEscape(id)+"?conversation_id="+url.QueryEscape(chat), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(client.Header, client.Secret)
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &APIError{Status: response.StatusCode, Code: "attachment_download"}
	}
	return io.ReadAll(io.LimitReader(response.Body, (64<<20)+1))
}
func (client *Client) SaveReplyAttachment(ctx context.Context, chat, run, key string) (harness.AttachmentRef, error) {
	var ref harness.AttachmentRef
	err := client.request(ctx, http.MethodPost, "/api/attachments/reply", map[string]string{"conversation_id": chat, "run_id": run, "request_key": key}, &ref)
	return ref, err
}
