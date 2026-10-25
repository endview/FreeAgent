package harness

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/endview/freeagent/internal/apppaths"
)

// OpenRuntime assembles only explicitly selected capabilities. An adapter
// supplied by a local caller replaces its default without moving reliability.
func OpenRuntime(ctx context.Context, dataDir string, adapters Adapters) (Config, *Store, *Service, func(), error) {
	none := func() {}
	config, err := LoadConfig(dataDir)
	if err != nil {
		return Config{}, nil, nil, none, err
	}
	if adapters.Keys == nil {
		path, err := apppaths.KeysFile()
		if err != nil {
			return Config{}, nil, nil, none, err
		}
		adapters.Keys = NewKeyStore(path)
	}
	store, err := OpenStore(ctx, dataDir)
	if err != nil {
		return Config{}, nil, nil, none, err
	}
	var tools *RuntimeTools
	var manager *ToolRuntimeManager
	closeAll := func() {
		if tools != nil {
			_ = tools.Close()
		}
		_ = store.Close()
	}
	if adapters.Tools == nil {
		files, err := NewFileTools(config.Workspace, dataDir)
		if err != nil {
			closeAll()
			return Config{}, nil, nil, none, err
		}
		tools = NewRuntimeTools(files)
		tools.SetKeyStore(adapters.Keys)
		tools.SetLogDir(filepath.Join(dataDir, "logs"))
		tools.SetDataDir(dataDir)
		// Command policy is owned by the runtime manager so it can be replaced
		// for future runs without mutating a view held by an active run.
		if config.HTTP.Enabled {
			tools.HTTP = &HTTPTools{Config: config.HTTP}
		}
		if err := tools.Connect(ctx, config, ""); err != nil {
			closeAll()
			return Config{}, nil, nil, none, err
		}
		adapters.Tools = tools
		manager = NewToolRuntimeManager(tools, config)
		adapters.Tools = manager
	}
	if adapters.Model == nil && adapters.ModelFor == nil {
		adapters.ModelFor = keyedModelClient(adapters.Keys)
	}
	if adapters.Refresh == nil {
		adapters.Refresh = func() (Config, error) { return LoadConfig(dataDir) }
	}
	service := NewService(store, config, adapters)
	service.controlToolsActive = true
	service.toolManager = manager
	service.commands, err = newCommandSupervisor(ctx, store)
	if err != nil {
		closeAll()
		return Config{}, nil, nil, none, err
	}
	closeAll = func() {
		_ = service.commands.close("owner_shutdown")
		if service != nil && service.extensions != nil {
			_ = service.extensions.close()
		}
		if tools != nil {
			_ = tools.Close()
		}
		// Sandboxed commands have stopped with the owner; the workspace
		// grants they shared are no longer needed.
		_ = releaseSandboxGrants(dataDir)
		_ = store.Close()
	}
	return config, store, service, closeAll, nil
}

// keyedModelClient resolves the model's key once per dispatch, so a rotated
// key takes effect on the next call and the plain key is never cached.
func keyedModelClient(keys *KeyStore) ModelFactory {
	return func(config ModelConfig) (ModelClient, error) {
		switch config.Provider {
		case "echo":
			return EchoModel{}, nil
		case "openai-compatible":
			if keys == nil {
				return nil, ErrKeyNotFound
			}
			key, err := keys.Resolve(config.Key)
			if err != nil {
				return nil, err
			}
			if key.Protocol != KeyProtocolOpenAICompatible {
				return nil, fmt.Errorf("harness: model provider and key protocol do not match")
			}
			var client ModelClient
			if config.EffectiveCapabilities().Protocol == "responses" {
				client, err = NewResponsesClient(config, key, &http.Client{Timeout: 2 * time.Minute})
			} else {
				client, err = NewOpenAICompatibleClient(config, key, &http.Client{Timeout: 2 * time.Minute})
			}
			if err != nil {
				return nil, err
			}
			return client, nil
		case "anthropic":
			if keys == nil {
				return nil, ErrKeyNotFound
			}
			key, err := keys.Resolve(config.Key)
			if err != nil {
				return nil, err
			}
			if key.Protocol != KeyProtocolAnthropic {
				return nil, fmt.Errorf("harness: model provider and key protocol do not match")
			}
			client, err := NewAnthropicClient(config, key, &http.Client{Timeout: 2 * time.Minute})
			if err != nil {
				return nil, err
			}
			return client, nil
		default:
			return nil, fmt.Errorf("harness: unsupported model provider %q", config.Provider)
		}
	}
}

func OpenTasks(ctx context.Context, dataDir string, adapters Adapters) (*TaskService, func(), error) {
	_, _, core, closeAll, err := OpenRuntime(ctx, dataDir, adapters)
	if err != nil {
		return nil, closeAll, err
	}
	tasks := NewTaskService(core)
	tasks.SetApproval(adapters.Approve)
	tasks.SetEvents(adapters.Emit)
	return tasks, closeAll, nil
}
