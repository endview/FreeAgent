package harness

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestLiveProviderAuxiliaryControlProposal(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	if config.Model != "deepseek-flash" {
		t.Skip("this real control gate requires the declared DeepSeek off switch")
	}
	config.Capabilities.Reasoning = ReasoningCapabilities{Control: "deepseek", Efforts: "low,high,max",
		Auxiliary: ReasoningSelection{Mode: "enabled", Effort: "high"}}
	live := keyedClient(t, config, keys, nil)
	core, _, _ := testHarness(t, live)
	core.controlToolsActive = true
	core.Model = modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		available := map[string]bool{}
		for _, definition := range request.Tools {
			available[definition.Name] = true
		}
		if !available[controlReadTool] || !available[controlPlanTool] {
			return ModelResponse{}, &CallError{Certain: true, Category: "capability", Message: "live control fixture did not offer control tools; no request dispatched"}
		}
		return live.Generate(ctx, request, emit)
	})
	core.Keys = keys
	core.Config.Models[core.Config.DefaultModel] = config
	enableTestProviders(&core.Config)
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	core.Refresh = func() (Config, error) { return LoadConfig(core.Store.DataDir()) }
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	result, err := tasks.Submit(ctx, chat, "auxiliary-setting-proposal",
		"请把当前配置模型的压缩/扩展分析思考关掉，主回答保持不变。先用控制工具读取当前状态，再用 model.set_profile 的辅助思考专用参数创建一个待我确认的提案。只使用 control_read_state 和 control_plan_change；不要读取或编辑文件，不要确认或应用提案。")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("real setting proposal not accepted: status=%s err=%v", result.Run.Status, err)
	}
	attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	read, proposed := false, false
	for _, attempt := range attempts {
		if attempt.Kind != "tool" {
			continue
		}
		var call ToolCall
		if json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil {
			t.Fatal("tool evidence malformed")
		}
		switch call.Name {
		case controlReadTool:
			read = attempt.Status == "finished"
		case controlPlanTool:
			var args struct {
				Kind      string                       `json:"kind"`
				Arguments modelProfileControlArguments `json:"arguments"`
			}
			if !read || json.Unmarshal(call.Arguments, &args) != nil || args.Kind != ControlSetModelProfile || args.Arguments.AuxiliaryReasoning == nil || *args.Arguments.AuxiliaryReasoning != (ReasoningSelection{Mode: "disabled"}) {
				t.Fatal("proposal did not use the current narrow control operation")
			}
			proposed = attempt.Status == "pending"
		default:
			t.Fatalf("setting request bypassed control tools: %s", call.Name)
		}
	}
	if !read || !proposed || !reflect.DeepEqual(core.Config.Models[core.Config.DefaultModel], config) {
		t.Fatal("read/proposal evidence absent or settings changed without confirmation")
	}
	t.Log("real DeepSeek used current control state and created the exact auxiliary-only proposal; settings unchanged pending user confirmation")
}
