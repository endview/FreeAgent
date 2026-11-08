package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
)

// RestoreExtensionPair restores into a new directory with the old Owner stopped.
// A later extension failure retains the valid Core restore, with this extension
// explicitly disabled. It never overwrites a running Core or replays requests.
func RestoreExtensionPair(ctx context.Context, source, target, packagePath string) (returnErr error) {
	manifest, err := VerifyExtensionPair(ctx, source)
	if err != nil {
		return err
	}
	info, err := CheckExtension(packagePath)
	if err != nil {
		return err
	}
	if info.ID != manifest.ID || info.Version != manifest.Version || info.StorageSchema != manifest.Schema || info.StorageVersion != manifest.StorageVersion || info.StateVersion != manifest.StateVersion {
		return errors.New("harness: matching extension package/version required")
	}
	if err = Restore(ctx, filepath.Join(source, "core"), target); err != nil {
		return err
	}
	config, err := LoadConfig(target)
	if err != nil {
		return err
	}
	wasEnabled := config.Extensions.Enabled[manifest.ID]
	config.Extensions.Enabled[manifest.ID] = false
	if err = WriteConfig(target, config); err != nil {
		return err
	}
	store, err := OpenStore(ctx, target)
	if err != nil {
		return err
	}
	defer store.Close()
	core := NewService(store, config, Adapters{Model: EchoModel{}})
	defer core.extensions.close()
	tasks := NewTaskService(core)
	if _, err = tasks.ImportExtension(ctx, packagePath); err != nil {
		return err
	}
	if err = postgres.ProvisionExtension(ctx, target, extensionStoragePath(target, manifest.ID), manifest.ID, manifest.Schema, manifest.StorageVersion); err != nil {
		return err
	}
	temporary := cloneConfig(tasks.Config())
	temporary.Extensions.Enabled[manifest.ID] = true
	core.setConfig(temporary)
	c, err := postgres.Read(extensionStoragePath(target, manifest.ID))
	if err != nil {
		return err
	}
	list, err := extensionRestoreList(ctx, c, filepath.Join(source, "extension.pgdump"))
	if err != nil {
		return err
	}
	toc, err := os.CreateTemp(filepath.Dir(target), ".extension-restore-list-*")
	if err != nil {
		return err
	}
	defer os.Remove(toc.Name())
	if _, err = toc.Write(list); err != nil {
		toc.Close()
		return err
	}
	if err = toc.Close(); err != nil {
		return err
	}
	command, err := postgres.Command(ctx, c, "pg_restore", "--exit-on-error", "--no-owner", "--no-privileges", "--use-list="+toc.Name(), "--dbname="+c.Database, filepath.Join(source, "extension.pgdump"))
	if err != nil {
		return err
	}
	if err = command.Run(); err != nil {
		return errors.New("harness: extension restore failed; valid Core retained, extension remains disabled")
	}
	var sources []ExtensionSource
	b, err := os.ReadFile(filepath.Join(source, "sources.json"))
	if err != nil || len(b) > 64<<20 || json.Unmarshal(b, &sources) != nil {
		return ErrStoreFormat
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	if err = verifyExtensionSources(ctx, tx, sources); err != nil {
		tx.Rollback()
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = copyRegularFile(filepath.Join(source, "control-journal.jsonl"), filepath.Join(target, controlJournalName)); err != nil {
		return err
	}
	if _, err = tasks.ExtensionState(ctx, manifest.ID, "", "", "owner"); err != nil {
		return err
	}
	temporary.Extensions.Enabled[manifest.ID] = wasEnabled
	return WriteConfig(target, temporary)
}
