package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

type attachmentClient interface {
	UploadAttachmentRequest(context.Context, string, string, string, io.Reader) (harness.AttachmentRef, error)
	Attachments(context.Context, string) ([]harness.AttachmentInfo, error)
	SaveReplyAttachment(context.Context, string, string, string) (harness.AttachmentRef, error)
}

func uploadSelectedFiles(ctx context.Context, client attachmentClient, chat, key string, paths []string) ([]string, error) {
	if len(paths) > 8 {
		return nil, usageErr("at most eight attachments")
	}
	var ids []string
	for i, path := range paths {
		file, err := harness.OpenUserAttachmentFile(path)
		if err != nil {
			return nil, err
		}
		ref, err := client.UploadAttachmentRequest(ctx, chat, filepath.Base(path), fmt.Sprintf("%s-%d", key, i), file)
		file.Close()
		if err != nil {
			return nil, err
		}
		ids = append(ids, ref.ID)
	}
	return ids, nil
}
func runAttachments(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("attachments <upload|list|download|save-reply> --session <id> [--file <local-file>|--id <id> --out <new-file>|--run <id>]")
	}
	flags := flag.NewFlagSet("attachments", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	chat := flags.String("session", "", "conversation id")
	filePath := flags.String("file", "", "explicit local upload file")
	id := flags.String("id", "", "attachment id")
	target := flags.String("out", "", "new local download path")
	run := flags.String("run", "", "reply Run id")
	key := flags.String("request", fmt.Sprintf("attachment-%d", time.Now().UnixNano()), "idempotency key")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *chat == "" {
		return usageErr("attachments requires --session")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	var client attachmentClient
	var tasks *harness.TaskService
	_, _, core, closeAll, err := openRuntime(ctx, *data)
	if errors.Is(err, harness.ErrOwnerActive) {
		remote, attachErr := interactiveOwner(ctx, *data)
		if attachErr != nil {
			return attachErr
		}
		client = remote
	} else if err != nil {
		return err
	} else {
		defer closeAll()
		tasks = harness.NewTaskService(core)
		client = tasks
	}
	switch args[0] {
	case "list":
		refs, err := client.Attachments(ctx, *chat)
		if err != nil {
			return err
		}
		return writeJSON(stdout, refs)
	case "upload":
		if *filePath == "" {
			return usageErr("upload requires --file")
		}
		file, err := harness.OpenUserAttachmentFile(*filePath)
		if err != nil {
			return err
		}
		defer file.Close()
		ref, err := client.UploadAttachmentRequest(ctx, *chat, filepath.Base(*filePath), *key, file)
		if err != nil {
			return err
		}
		return writeJSON(stdout, ref)
	case "save-reply":
		if *run == "" {
			return usageErr("save-reply requires --run")
		}
		ref, err := client.SaveReplyAttachment(ctx, *chat, *run, *key)
		if err != nil {
			return err
		}
		return writeJSON(stdout, ref)
	case "download":
		if *target == "" || *id == "" {
			return usageErr("download requires --id and --out; existing files are never overwritten")
		}
		var raw []byte
		if tasks != nil {
			_, raw, err = tasks.Attachment(ctx, *chat, *id)
		} else {
			raw, err = client.(interface {
				DownloadAttachment(context.Context, string, string) ([]byte, error)
			}).DownloadAttachment(ctx, *chat, *id)
		}
		if err != nil {
			return err
		}
		if len(raw) > 64<<20 {
			return errors.New("attachment download exceeded limit")
		}
		path, err := filepath.Abs(*target)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(raw)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"saved": path, "bytes": len(raw)})
	default:
		return usageErr("unknown attachments action; delete uses control plan --kind attachments.delete with exact id/hash")
	}
}
