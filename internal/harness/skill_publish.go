package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/endview/freeagent/internal/fileops"
)

type SkillPublication struct {
	ID             string `json:"id"`
	Document       string `json:"document"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

func (service *ControlService) validateSkillPublication(raw json.RawMessage) (SkillPublication, string, error) {
	var args SkillPublication
	if err := decodeControlJSON(raw, &args); err != nil || !validCapabilityID(args.ID) || len(args.Document) > 64<<10 {
		return args, "", errors.New("harness: invalid bounded Skill publication")
	}
	name, _, trigger, _, _, err := parseSkillDocument([]byte(args.Document))
	if err != nil || name != args.ID || trigger != skillTriggerManual {
		return args, "", errors.New("harness: publication requires matching Skill name and trigger: manual")
	}
	if service.core.redactSecrets(args.Document) != args.Document {
		return args, "", errors.New("harness: remove credentials before publishing this Skill")
	}
	path := filepath.Join(service.core.Store.DataDir(), "skills", args.ID)
	_, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if args.ExpectedSHA256 != "" {
			return args, "", ErrApprovalChanged
		}
		return args, "", nil
	}
	item, err := readSkill(path, "user")
	if err != nil {
		return args, "", err
	}
	if args.ExpectedSHA256 != item.SHA256 {
		return args, "", ErrApprovalChanged
	}
	if service.core.currentConfig().Skills.Enabled[args.ID] {
		return args, "", errors.New("harness: disable this Skill before replacing its document")
	}
	if len(item.Files) > 0 {
		return args, "", errors.New("harness: document publication cannot replace a Skill that contains supporting files")
	}
	old, err := readBoundedFile(filepath.Join(path, "SKILL.md"), maxSkillDocumentBytes)
	return args, string(old), err
}

func (service *ControlService) buildSkillPublication(ctx context.Context, record *controlRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	args, old, err := service.validateSkillPublication(record.Arguments)
	if err != nil {
		return err
	}
	resource := "skills.catalog:" + args.ID
	record.Operation.ResourceIDs = []string{resource}
	record.Before = map[string]json.RawMessage{resource: mustJSON(old)}
	record.After = map[string]json.RawMessage{resource: mustJSON(args.Document)}
	record.Operation.OldValues = map[string]string{resource: service.core.redactSecrets(old)}
	record.Operation.ProposedValues = map[string]string{resource: args.Document}
	record.Operation.Preview = fmt.Sprintf("Publish skills/%s/SKILL.md (%d UTF-8 bytes, estimated %d context tokens if later selected). Stored disabled; no commands or supporting scripts are executed.\n\n%s", args.ID, len(args.Document), estimateContextTokens(args.Document), args.Document)
	return nil
}

func (service *ControlService) applySkillPublication(ctx context.Context, record *controlRecord) error {
	args, old, err := service.validateSkillPublication(record.Arguments)
	if err != nil {
		return err
	}
	resource := "skills.catalog:" + args.ID
	if string(record.Before[resource]) != string(mustJSON(old)) {
		return ErrApprovalChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root := filepath.Join(service.core.Store.DataDir(), "skills")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if rootInfo, err := os.Lstat(root); err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("harness: invalid Skill root")
	}
	target := filepath.Join(root, args.ID)
	if old == "" {
		staging, err := os.MkdirTemp(root, ".publish-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		if err := writePublishedSkill(filepath.Join(staging, "SKILL.md"), args.Document); err != nil {
			return err
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			return ErrApprovalChanged
		}
		return fileops.Rename(staging, target)
	}
	// Preserve the reviewed previous document for explicit recovery, without
	// putting revisions into the active Skill's discovered file list.
	archive := filepath.Join(root, ".revisions", args.ID)
	for _, directory := range []string{filepath.Join(root, ".revisions"), archive} {
		if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("harness: invalid Skill revision directory")
		}
	}
	history := filepath.Join(archive, args.ExpectedSHA256+".md")
	if _, err := os.Lstat(history); errors.Is(err, os.ErrNotExist) {
		if err := writePublishedSkill(history, old); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		info, err := os.Lstat(history)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("harness: invalid saved Skill revision")
		}
		previous, err := readBoundedFile(history, maxSkillDocumentBytes)
		if err != nil || contentHash(previous) != args.ExpectedSHA256 {
			return ErrApprovalChanged
		}
	}
	stage, err := os.CreateTemp(root, ".publish-")
	if err != nil {
		return err
	}
	name := stage.Name()
	defer os.Remove(name)
	if _, err = stage.WriteString(args.Document); err != nil {
		stage.Close()
		return err
	}
	if err = stage.Sync(); err != nil {
		stage.Close()
		return err
	}
	if err = stage.Close(); err != nil {
		return err
	}
	if _, _, err := service.validateSkillPublication(record.Arguments); err != nil {
		return err
	}
	if err := fileops.Rename(name, filepath.Join(target, "SKILL.md")); err != nil {
		return err
	}
	return nil
}
func writePublishedSkill(path, document string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(document)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}
