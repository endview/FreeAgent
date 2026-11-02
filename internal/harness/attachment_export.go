package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

func attachmentExportDefinition() ToolDefinition {
	return ToolDefinition{Name: "file_export", Effect: EffectModify, Description: "Publish an immutable download snapshot of one authorized regular file (at most 32 MiB by default). Preparation binds the exact source hash; approval follows the current file permission mode. A generated file is not delivered until its attachment receipt is returned.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}
}

func (files *FileTools) exportBytes(path string) ([]byte, error) {
	name, err := files.validatePath(path)
	if err != nil {
		return nil, err
	}
	file, err := openWorkspaceFile(files.root, filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := checkRegularHandle(file); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() > 64<<20 {
		return nil, errors.New("harness: file exceeds export limit")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	if err != nil || len(raw) > 64<<20 {
		return nil, errors.New("harness: cannot read complete export source")
	}
	return raw, nil
}
func (files *FileTools) prepareExport(ctx context.Context, call ToolCall) (PreparedTool, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArguments(call.Arguments, &args); err != nil {
		return PreparedTool{}, err
	}
	raw, err := files.exportBytes(args.Path)
	if err != nil {
		return PreparedTool{}, err
	}
	hash := contentHash(raw)
	return PreparedTool{Effect: EffectModify, workspaceFile: !files.protectedConfig(args.Path), Preview: fmt.Sprintf("Export immutable copy of %s\n%d bytes\nSHA-256: %s", args.Path, len(raw), hash), Items: []ApprovalItem{{Kind: "file_export", Target: args.Path, Hash: hash}}, exportName: attachmentName(args.Path), exportSize: int64(len(raw)), exportRead: func(ctx context.Context) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := files.exportBytes(args.Path)
		if err != nil || contentHash(raw) != hash {
			return nil, errors.New("harness: export source changed; preview again")
		}
		return raw, nil
	}}, nil
}

type attachmentExportTools struct {
	base    ToolRunner
	service *Service
	runID   string
}

func (tools attachmentExportTools) Definitions() []ToolDefinition { return tools.base.Definitions() }
func (tools attachmentExportTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	prepared, err := tools.base.Prepare(ctx, call)
	if err != nil || call.Name != "file_export" {
		return prepared, err
	}
	if prepared.exportRead == nil {
		return PreparedTool{}, errors.New("harness: file export must use the file broker")
	}
	if prepared.exportSize > tools.service.attachmentPolicy().MaxExportBytes {
		if prepared.release != nil {
			prepared.release()
		}
		return PreparedTool{}, errors.New("harness: file exceeds configured export limit")
	}
	prepared.Execute = func(ctx context.Context) (ToolResult, error) {
		if prepared.release != nil {
			defer prepared.release()
		}
		raw, err := prepared.exportRead(ctx)
		if err != nil {
			return ToolResult{}, &CallError{Certain: true, Message: err.Error()}
		}
		run, err := tools.service.Store.GetRun(ctx, tools.runID)
		if err != nil {
			return ToolResult{}, err
		}
		ref, err := NewTaskService(tools.service).createAttachment(ctx, run.ConversationID, prepared.exportName, bytes.NewReader(raw), "file_export", run.ID, true)
		if err != nil {
			return ToolResult{}, &CallError{Certain: true, Message: err.Error()}
		}
		result, _ := jsonResult(map[string]any{"attachment": ref, "published": true, "delivered": false})
		result.Attachments = []AttachmentRef{ref}
		result.Receipts = []ToolReceipt{{Kind: "attachment", Path: ref.ID, Status: "applied", AfterHash: ref.SHA256, Detail: "immutable snapshot published; transport delivery is separate"}}
		return result, nil
	}
	prepared.Reject = func() ToolResult {
		return ToolResult{Content: "user declined this export; do not repeat it", IsError: true}
	}
	return prepared, nil
}
