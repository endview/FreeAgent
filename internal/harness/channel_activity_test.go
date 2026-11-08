package harness

import (
	"context"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestChannelActivityRequiresLiveAuthorizedPrivateRun(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return ModelResponse{Content: "done"}, nil
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		}
	}))
	tasks := NewTaskService(core)
	key, err := tasks.ChannelKeys().Add(KeyInput{Name: "Bot", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: "saved", Secret: []byte("1:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	connection := channelwire.Connection{ID: "tg", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true}
	core.Config.Channels = ChannelsConfig{Connections: []channelwire.Connection{connection}}
	b, err := newTestChannelBridge(tasks, "http://127.0.0.1:1", "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := tasks.SubmitChannel(ctx, input); done <- err }()
	<-entered
	activities, err := b.Activities(t.Context(), "tg")
	if err != nil || len(activities) != 1 || activities[0].Target.ChatID != "90" || activities[0].Action != "typing" {
		t.Fatal(activities, err)
	}
	connection.DisableTyping = true
	if err := tasks.ConfigureChannels(t.Context(), ChannelsConfig{Connections: []channelwire.Connection{connection}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Configure(); err != nil {
		t.Fatal(err)
	}
	activities, err = b.Activities(t.Context(), "tg")
	if err != nil || len(activities) != 0 {
		t.Fatal("disabled indicator remained active", activities, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	connection.DisableTyping = false
	if err := tasks.ConfigureChannels(t.Context(), ChannelsConfig{Connections: []channelwire.Connection{connection}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Configure(); err != nil {
		t.Fatal(err)
	}
	activities, err = b.Activities(t.Context(), "tg")
	if err != nil || len(activities) != 0 {
		t.Fatal("completed run still typing", activities, err)
	}
}
