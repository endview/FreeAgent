package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
)

func attachmentExportDefinition() ToolDefinition {
	return ToolDefinition{Name: "file_export", Effect: EffectModify, Description: "Create an immutable downloadable attachment of an authorized regular file (32 MiB default). The download card is ready immediately; channel delivery occurs when this Run ends or reaches a limit. Do not poll or repeat export to wait for delivery. Final channel receipts determine sent/failed/unknown. No hash in the reply or source file is required. Repeated exports of one source select its latest unsent version; keep_version is only for user-requested multiple versions.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"keep_version":{"type":"boolean","description":"Retain this version for delivery when the user explicitly requested multiple versions"}},"required":["path"],"additionalProperties":false}`)}
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
		Path        string `json:"path"`
		KeepVersion bool   `json:"keep_version,omitempty"`
	}
	if err := decodeArguments(call.Arguments, &args); err != nil {
		return PreparedTool{}, err
	}
	raw, err := files.exportBytes(args.Path)
	if err != nil {
		return PreparedTool{}, err
	}
	hash := contentHash(raw)
	canonical := filepath.Clean(filepath.Join(files.workspace, filepath.FromSlash(args.Path)))
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	return PreparedTool{Effect: EffectModify, exportSource: contentHash([]byte(canonical)), exportKeepVersion: args.KeepVersion, workspaceFile: !files.protectedConfig(args.Path), Preview: fmt.Sprintf("Export immutable copy of %s\n%d bytes\nSHA-256: %s\nKeep separate delivery version: %t", args.Path, len(raw), hash, args.KeepVersion), Items: []ApprovalItem{{Kind: "file_export", Target: args.Path, Hash: hash}}, exportName: attachmentName(args.Path), exportSize: int64(len(raw)), exportRead: func(ctx context.Context) ([]byte, error) {
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
		ref, err := NewTaskService(tools.service).createAttachment(ctx, run.ConversationID, prepared.exportName, bytes.NewReader(raw), "file_export", run.ID, true, exportSelection{Source: prepared.exportSource, KeepVersion: prepared.exportKeepVersion})
		if err != nil {
			return ToolResult{}, &CallError{Certain: true, Message: err.Error()}
		}
		delivery := map[string]string{"state": "download_ready", "message": "Authenticated download card is ready. No hash needs to be copied into the reply or source file."}
		if tools.service.channel != nil {
			delivery = map[string]string{"state": "pending", "when": "run_end_or_limit", "message": "Snapshot ready; channel delivery is scheduled after this Run ends or reaches a limit. Do not repeat export or poll while answering. The final transport receipt is authoritative."}
		}
		result, _ := jsonResult(map[string]any{"attachment": ref, "download_ready": true, "delivery": delivery})
		result.Attachments = []AttachmentRef{ref}
		result.Receipts = []ToolReceipt{{Kind: "attachment", Path: ref.ID, Status: "applied", AfterHash: ref.SHA256, Detail: "immutable snapshot published; transport delivery is separate"}}
		return result, nil
	}
	prepared.Reject = func() ToolResult {
		return ToolResult{Content: "user declined this export; do not repeat it", IsError: true}
	}
	return prepared, nil
}
