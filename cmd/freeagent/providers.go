package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/endview/freeagent/internal/harness"
)

func changeProvider(ctx context.Context, data, ref string, enabled bool, input io.Reader, out, errOut io.Writer) error {
	keys, err := keyStore()
	if err != nil {
		return err
	}
	profile, err := keys.Find(ref)
	if err != nil {
		return err
	}
	if harness.IsChannelKeyProtocol(profile.Protocol) {
		return usageErr("channel credentials use channel configure, not Provider switches")
	}
	args, _ := json.Marshal(map[string]any{"id": profile.ID, "enabled": enabled})
	store, err := harness.OpenStore(ctx, data)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, err := interactiveOwner(ctx, data)
		if err != nil {
			return err
		}
		return runAttachedControl(ctx, client, "plan", "", harness.ControlSetProviderEnabled, string(args), "", "", input, out, errOut, true)
	}
	if err != nil {
		return err
	}
	defer store.Close()
	config, err := harness.LoadConfig(data)
	if err != nil {
		return err
	}
	tasks := harness.NewTaskService(harness.NewService(store, config, harness.Adapters{Keys: keys}))
	control, err := tasks.Control()
	if err != nil {
		return err
	}
	result, err := control.ApplyDirectControlChange(ctx, harness.ControlSetProviderEnabled, json.RawMessage(args), "", "cli")
	if err != nil {
		return err
	}
	return writeJSON(out, result)
}
