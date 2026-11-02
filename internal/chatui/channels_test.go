package chatui

import (
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChannelPairingRequiresInteractiveSurfaceAndUsesOwnerLifetime(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"fixture_bot"}}`)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
			fmt.Fprint(w, `{"ok":true,"result":[]}`)
		}
	}))
	defer remote.Close()
	key, err := ui.tasks.Keys().Add(harness.KeyInput{Name: "fixture bot", BaseURL: remote.URL, Protocol: harness.KeyProtocolTelegram, Source: "saved", Secret: []byte("12345678:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	runtime := harness.NewTelegramRuntime(ui.tasks)
	ui.server.SetTelegramRuntime(runtime)
	runtime.SetDispatch(ui.server.DispatchChannel)
	ui.server.AddChannelDelivery(runtime.Deliver)
	t.Cleanup(func() { runtime.Stop() })
	code, raw := ui.request(t, "PATCH", "/api/channels/telegram", map[string]any{"enabled": true, "key": key.ID})
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	deadline := time.Now().Add(2 * time.Second)
	for !runtime.Status().Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !runtime.Status().Running {
		t.Fatalf("request lifetime stopped optional channel: %+v", runtime.Status())
	}
	pair, _, err := runtime.PairCode()
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), "GET", ui.http.URL+"/api/attach/channels", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Attach", ui.server.attachToken)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || strings.Contains(string(body), pair) || strings.Contains(string(body), "pair_code") {
		t.Fatalf("observer exposed pairing capability: %s", body)
	}
	code, raw = ui.request(t, "GET", "/api/channels", nil)
	if code != 200 || !strings.Contains(string(raw), pair) {
		t.Fatalf("interactive surface lost pairing: %s", raw)
	}

}
