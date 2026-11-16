package harness

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
)

// A tool's trusted scope is supplied by the host, never accepted from model
// arguments. Remote entry facts survive restarts in the existing Run metadata.
type extensionToolScope struct {
	CoreID         string `json:"core_id"`
	SpaceID        string `json:"space_id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	Local          bool   `json:"local"`
	Vision         bool   `json:"vision"`
}

func (service *Service) extensionToolScope(ctx context.Context, run Run) (extensionToolScope, error) {
	scope := extensionToolScope{RunID: run.ID, ConversationID: run.ConversationID}
	chat, err := service.Store.GetConversation(ctx, run.ConversationID)
	if err != nil {
		return scope, err
	}
	scope.SpaceID = chat.SpaceID
	if err = service.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key='core/id'`).Scan(&scope.CoreID); err != nil {
		return scope, err
	}
	var remote bool
	if err = service.Store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, "run/remote/"+run.ID).Scan(&remote); err != nil {
		return scope, err
	}
	scope.Local = !remote && service.remoteEntry == nil && service.channel == nil && channelOriginFromJSON(run.ConfigJSON) == nil
	if binding, e := service.binding(); e == nil {
		scope.Vision = binding.Config.EffectiveCapabilities().Vision.Enabled
	}
	return scope, nil
}

// Inline bytes are bounded and verified; extensions cannot hand the host an
// arbitrary local path or choose another conversation's attachment reference.
type extensionToolArtifact struct {
	Name   string `json:"name"`
	Base64 string `json:"base64"`
	Image  bool   `json:"image,omitempty"`
}

func (service *Service) extensionArtifacts(ctx context.Context, run Run, id string, artifacts []extensionToolArtifact) ([]AttachmentRef, error) {
	if len(artifacts) == 0 {
		return nil, nil
	}
	if len(artifacts) > 4 {
		return nil, ErrAttachmentLimit
	}
	blobs := make([][]byte, len(artifacts))
	total := 0
	policy := service.attachmentPolicy()
	for i, a := range artifacts {
		if a.Name == "" || len(a.Name) > 512 || len(a.Base64) > 12<<20 {
			return nil, ErrAttachmentLimit
		}
		b, e := base64.StdEncoding.Strict().DecodeString(a.Base64)
		if e != nil || len(b) == 0 || len(b) > 8<<20 {
			return nil, ErrAttachmentLimit
		}
		total += len(b)
		if total > 8<<20 {
			return nil, ErrAttachmentLimit
		}
		if a.Image {
			c, format, e := image.DecodeConfig(bytes.NewReader(b))
			if e != nil || format != "png" && format != "jpeg" || c.Width <= 0 || c.Height <= 0 || int64(c.Width) > policy.MaxPixels/int64(c.Height) || format == "png" && !policy.PNG || format == "jpeg" && !policy.JPEG {
				return nil, ErrAttachment
			}
			if _, _, e = image.Decode(bytes.NewReader(b)); e != nil {
				return nil, ErrAttachment
			}
		}
		blobs[i] = b
	}
	scope, e := service.extensionToolScope(ctx, run)
	if e != nil {
		return nil, e
	}
	refs := make([]AttachmentRef, 0, len(artifacts))
	for i, a := range artifacts {
		source := "tool_export"
		if a.Image && scope.Vision {
			source = "tool_image"
		}
		ref, e := NewTaskService(service).createAttachment(WithAttachmentIdentity(ctx, "extension:"+id), run.ConversationID, a.Name, bytes.NewReader(blobs[i]), source, run.ID, true)
		if e != nil {
			return nil, e
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
