//go:build windows

package winlaunch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceNetworkHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--service-network-helper" {
			continue
		}
		if os.Args[i+1] == "client" {
			client := &http.Client{Timeout: time.Second}
			resp, err := client.Get(os.Args[i+2])
			if err != nil {
				fmt.Println("client_error", err)
				os.Exit(3)
			}
			defer resp.Body.Close()
			_, _ = io.Copy(os.Stdout, resp.Body)
			os.Exit(0)
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			fmt.Println("listen_error", err)
			os.Exit(2)
		}
		fmt.Println("http://" + listener.Addr().String())
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "SERVICE_NETWORK_OK") }))
		os.Exit(0)
	}
}

// This is a platform probe, not a promise of sandbox loopback support. It
// records host and same-profile command reachability without adding exemptions.
func TestServiceNetworkPrototype(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{ModeHost, ModeAppContainer} {
		for _, network := range []NetworkMode{NetworkOff, NetworkInternet} {
			if mode == ModeHost && network == NetworkInternet {
				continue
			}
			t.Run(string(mode)+"/"+string(network), func(t *testing.T) {
				root := t.TempDir()
				spec := Spec{Command: program, Args: []string{"-test.run=TestServiceNetworkHelper", "--", "--service-network-helper", "server"}, Dir: root, Mode: mode, Network: network, Env: os.Environ()}
				if mode == ModeAppContainer {
					spec.AppContainerName = fmt.Sprintf("FreeAgent.ServiceProbe.%d", time.Now().UnixNano())
					spec.Grants = []Grant{{Root: root, Access: AccessWrite}, {Root: filepath.Dir(program), Access: AccessRead}}
				}
				started := time.Now()
				p, err := Start(t.Context(), spec)
				if err != nil {
					t.Logf("NOT_SUPPORTED start: %v", err)
					if mode == ModeHost {
						t.Fatal(err)
					}
					return
				}
				defer p.Close()
				line := make(chan string, 1)
				go func() { text, _ := bufio.NewReader(p.Stdout()).ReadString('\n'); line <- strings.TrimSpace(text) }()
				var address string
				select {
				case address = <-line:
				case <-time.After(5 * time.Second):
					t.Fatal("no startup observation")
				}
				t.Logf("backend=%s network=%s pid=%d start_ms=%d observed=%s", mode, network, p.PID(), time.Since(started).Milliseconds(), address)
				if !strings.HasPrefix(address, "http://127.0.0.1:") {
					t.Log("NOT_SUPPORTED loopback listen")
					return
				}
				client := &http.Client{Timeout: time.Second}
				resp, err := client.Get(address)
				if err != nil {
					t.Logf("NOT_SUPPORTED host loopback: %v", err)
				} else {
					raw, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					t.Logf("host_http=%d body=%s", resp.StatusCode, raw)
					if string(raw) != "SERVICE_NETWORK_OK" {
						t.Fatal("wrong service")
					}
				}
				if mode == ModeHost && err != nil {
					t.Fatal(err)
				}
				spec.Args = []string{"-test.run=TestServiceNetworkHelper", "--", "--service-network-helper", "client", address}
				probe, err := Start(t.Context(), spec)
				if err != nil {
					t.Fatal(err)
				}
				defer probe.Close()
				ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
				exit, waitErr := probe.Wait(ctx)
				cancel()
				if waitErr != nil {
					_ = probe.Kill()
				}
				raw, _ := io.ReadAll(probe.Stdout())
				t.Logf("command_exit=%d error=%v output=%s", exit.Code, waitErr, raw)
				if mode == ModeHost && (waitErr != nil || exit.Code != 0) {
					t.Fatal("host command cannot reach service")
				}
				_ = p.Kill()
				ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if err := p.WaitTree(ctx); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
