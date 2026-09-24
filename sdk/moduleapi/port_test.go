package moduleapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestS1PortRefsAreExactAndReturnedAsCopy(t *testing.T) {
	want := []PortRef{
		{Name: PortNameModelGenerate, ExactVersion: PortVersionV2},
		{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		{Name: PortNameChannelTransport, ExactVersion: PortVersionV1},
	}
	first := S1PortRefs()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("S1PortRefs() = %+v, want %+v", first, want)
	}
	first[0].Name = "changed.by.caller"
	if got := S1PortRefs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutated S1 port refs: %+v", got)
	}
}

func TestNewPortPlanPreservesOrderedMultiBindingOrder(t *testing.T) {
	first := validPortBinding("context.role", "instance-z", FailureRequired)
	first.StaticContextRefs = []string{
		strings.Repeat("d", SHA256HexLength),
		strings.Repeat("e", SHA256HexLength),
	}
	second := validPortBinding("skill.static", "instance-a", FailureOptional)
	second.StaticContextRefs = []string{
		strings.Repeat("f", SHA256HexLength),
	}
	input := PortPlan{
		Port: PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{
			first,
			second,
		},
	}

	plan, err := NewPortPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Bindings, []PortBinding{first, second}) {
		t.Fatalf("binding order changed: %+v", plan.Bindings)
	}

	input.Bindings[0] = second
	if !reflect.DeepEqual(plan.Bindings, []PortBinding{first, second}) {
		t.Fatalf("constructed plan aliases caller bindings: %+v", plan.Bindings)
	}
	input.Bindings = []PortBinding{first, second}
	input.Bindings[0].StaticContextRefs[0] = strings.Repeat(
		"f",
		SHA256HexLength,
	)
	if plan.Bindings[0].StaticContextRefs[0] !=
		strings.Repeat("d", SHA256HexLength) {
		t.Fatal("constructed plan aliases caller static context refs")
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStaticContextRefsAreContextOnlyCanonicalAndUnique(t *testing.T) {
	contextBinding := validPortBinding(
		"context.role",
		"role-a",
		FailureRequired,
	)
	contextBinding.StaticContextRefs = []string{
		strings.Repeat("d", SHA256HexLength),
	}
	contextPlan, err := NewPortPlan(PortPlan{
		Port: PortRef{
			Name:         PortNameContextProvide,
			ExactVersion: PortVersionV1,
		},
		Bindings: []PortBinding{contextBinding},
	})
	if err != nil {
		t.Fatalf("context static refs rejected: %v", err)
	}
	if contextPlan.Bindings[0].StaticContextRefs == nil {
		t.Fatal("static context refs did not freeze as an explicit array")
	}

	modelBinding := validPortBinding(
		"model.deepseek",
		"model-a",
		FailureRequired,
	)
	modelBinding.StaticContextRefs = append(
		[]string{},
		contextBinding.StaticContextRefs...,
	)
	if _, err := NewPortPlan(PortPlan{
		Port: PortRef{
			Name:         PortNameModelGenerate,
			ExactVersion: PortVersionV2,
		},
		Bindings: []PortBinding{modelBinding},
	}); err == nil {
		t.Fatal("model binding accepted static context refs")
	}

	duplicate := contextBinding
	duplicate.StaticContextRefs = append(
		duplicate.StaticContextRefs,
		duplicate.StaticContextRefs[0],
	)
	if _, err := NewPortPlan(PortPlan{
		Port: PortRef{
			Name:         PortNameContextProvide,
			ExactVersion: PortVersionV1,
		},
		Bindings: []PortBinding{duplicate},
	}); err == nil {
		t.Fatal("duplicate static context ref was accepted")
	}

	invalid := contextBinding
	invalid.StaticContextRefs = []string{"not-a-digest"}
	if _, err := NewPortPlan(PortPlan{
		Port: PortRef{
			Name:         PortNameContextProvide,
			ExactVersion: PortVersionV1,
		},
		Bindings: []PortBinding{invalid},
	}); err == nil {
		t.Fatal("invalid static context ref was accepted")
	}
}

func TestModelGenerateRequiresExactlyOneRequiredBinding(t *testing.T) {
	valid := PortPlan{
		Port: PortRef{Name: PortNameModelGenerate, ExactVersion: PortVersionV2},
		Bindings: []PortBinding{
			validPortBinding("model.deepseek", "model-a", FailureRequired),
		},
	}
	if _, err := NewPortPlan(valid); err != nil {
		t.Fatalf("valid model plan was rejected: %v", err)
	}

	optional := valid
	optional.Bindings = append([]PortBinding(nil), valid.Bindings...)
	optional.Bindings[0].FailurePolicy = FailureOptional
	if _, err := NewPortPlan(optional); err == nil {
		t.Fatal("OPTIONAL model binding was accepted")
	}

	repeated := valid
	repeated.Bindings = append(repeated.Bindings, valid.Bindings[0])
	if _, err := NewPortPlan(repeated); err == nil {
		t.Fatal("multiple model.generate/v2 bindings were accepted")
	}
}

func TestActionProviderRequiresOrderedExecutableRequiredBindings(t *testing.T) {
	first := validPortBinding("action.text", "action-a", FailureRequired)
	first.Provider.ExecutionClass = ExecutionTrustedInProcess
	first.Provider.AdapterIdentity = "builtin.action-text"
	second := validPortBinding("action.files", "action-b", FailureRequired)
	second.Provider.ExecutionClass = ExecutionTrustedInProcess
	second.Provider.AdapterIdentity = "builtin.action-files"
	plan, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{first, second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Bindings, []PortBinding{first, second}) {
		t.Fatalf("action binding order changed: %+v", plan.Bindings)
	}

	localProcess := first
	localProcess.Provider.ExecutionClass = ExecutionLocalProcess
	localProcess.Provider.AdapterIdentity = "host.mcp-stdio"
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{localProcess},
	}); err != nil {
		t.Fatalf("LOCAL_PROCESS action binding was rejected: %v", err)
	}

	optional := first
	optional.FailurePolicy = FailureOptional
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{optional},
	}); err == nil {
		t.Fatal("OPTIONAL action binding was accepted")
	}

	declarative := first
	declarative.Provider.ExecutionClass = ExecutionDeclarative
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{declarative},
	}); err == nil {
		t.Fatal("DECLARATIVE action binding was accepted")
	}

	remote := first
	remote.Provider.ExecutionClass = ExecutionRemote
	remote.Provider.AdapterIdentity = "host.remote-action-http"
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{remote},
	}); err != nil {
		t.Fatalf("REMOTE action binding was rejected: %v", err)
	}

	wasm := first
	wasm.Provider.ExecutionClass = ExecutionWASM
	wasm.Provider.AdapterIdentity = "host.wasm-action"
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{wasm},
	}); err != nil {
		t.Fatalf("WASM action binding was rejected: %v", err)
	}

	withStatic := first
	withStatic.StaticContextRefs = []string{strings.Repeat("d", SHA256HexLength)}
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{withStatic},
	}); err == nil {
		t.Fatal("action binding with static context refs was accepted")
	}
}

func TestNewPortPlanRejectsEmptyUnknownAndInexactPorts(t *testing.T) {
	binding := validPortBinding("context.role", "role-a", FailureRequired)
	tests := []PortPlan{
		{
			Port: PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		},
		{
			Port:     PortRef{Name: "tool.execute", ExactVersion: PortVersionV1},
			Bindings: []PortBinding{binding},
		},
		{
			Port:     PortRef{Name: PortNameContextProvide, ExactVersion: ">=v1"},
			Bindings: []PortBinding{binding},
		},
		{
			Port:     PortRef{Name: "Context.Provide", ExactVersion: PortVersionV1},
			Bindings: []PortBinding{binding},
		},
	}
	for index, plan := range tests {
		if _, err := NewPortPlan(plan); err == nil {
			t.Fatalf("invalid plan %d was accepted: %+v", index, plan)
		}
	}
}

func TestNewPortPlanRejectsInvalidBindingFields(t *testing.T) {
	base := PortPlan{
		Port: PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{
			validPortBinding("context.role", "role-a", FailureRequired),
		},
	}
	base.Bindings[0].StaticContextRefs = []string{
		strings.Repeat("d", SHA256HexLength),
	}

	tests := []struct {
		name   string
		mutate func(*PortBinding)
	}{
		{
			name: "module id",
			mutate: func(binding *PortBinding) {
				binding.Provider.ModuleID = "Context.Role"
			},
		},
		{
			name: "version",
			mutate: func(binding *PortBinding) {
				binding.Provider.Version = "^1"
			},
		},
		{
			name: "artifact digest",
			mutate: func(binding *PortBinding) {
				binding.Provider.ArtifactDigest = strings.Repeat("A", SHA256HexLength)
			},
		},
		{
			name: "instance id",
			mutate: func(binding *PortBinding) {
				binding.Provider.InstanceID = " role-a"
			},
		},
		{
			name: "execution class",
			mutate: func(binding *PortBinding) {
				binding.Provider.ExecutionClass = "SANDBOX"
			},
		},
		{
			name: "adapter identity",
			mutate: func(binding *PortBinding) {
				binding.Provider.AdapterIdentity = ""
			},
		},
		{
			name: "activation revision",
			mutate: func(binding *PortBinding) {
				binding.Provider.ActivationRevision = 0
			},
		},
		{
			name: "config ref",
			mutate: func(binding *PortBinding) {
				binding.ConfigRef = "config-a"
			},
		},
		{
			name: "authority ceiling ref",
			mutate: func(binding *PortBinding) {
				binding.AuthorityCeilingRef = "authority-a"
			},
		},
		{
			name: "failure policy",
			mutate: func(binding *PortBinding) {
				binding.FailurePolicy = "BEST_EFFORT"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			plan.Bindings = append([]PortBinding(nil), base.Bindings...)
			test.mutate(&plan.Bindings[0])
			if _, err := NewPortPlan(plan); err == nil {
				t.Fatalf("invalid %s was accepted", test.name)
			}
		})
	}
}

func TestNewPortPlanRejectsExternalExecutionClassesOnNonActionPorts(t *testing.T) {
	for _, port := range []PortRef{
		{Name: PortNameModelGenerate, ExactVersion: PortVersionV2},
		{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		{Name: PortNameChannelTransport, ExactVersion: PortVersionV1},
	} {
		for _, class := range []ExecutionClass{ExecutionLocalProcess, ExecutionRemote, ExecutionWASM} {
			t.Run(port.Name+"/"+string(class), func(t *testing.T) {
				binding := validPortBinding("context.role", "role-a", FailureRequired)
				binding.Provider.ExecutionClass = class
				_, err := NewPortPlan(PortPlan{
					Port:     port,
					Bindings: []PortBinding{binding},
				})
				if err == nil || !strings.Contains(err.Error(), "execution class") {
					t.Fatalf("port %s accepted execution class %q: %v", port.Name, class, err)
				}
			})
		}
	}
}

func TestContextProvideFreezesDeclarativeAndDynamicShapes(t *testing.T) {
	declarative := validPortBinding(
		"context.role",
		"role-a",
		FailureRequired,
	)
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{declarative},
	}); err == nil {
		t.Fatal("DECLARATIVE context Binding without static refs was accepted")
	}

	dynamic := validPortBinding(
		"context.knowledge",
		"knowledge-a",
		FailureRequired,
	)
	dynamic.Provider.ExecutionClass = ExecutionTrustedInProcess
	dynamic.Provider.AdapterIdentity = "builtin.deterministic-knowledge"
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{dynamic},
	}); err != nil {
		t.Fatalf("valid dynamic context Binding was rejected: %v", err)
	}

	withStatic := dynamic
	withStatic.StaticContextRefs = []string{
		strings.Repeat("d", SHA256HexLength),
	}
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{withStatic},
	}); err == nil {
		t.Fatal("dynamic context Binding with static refs was accepted")
	}

	optional := dynamic
	optional.FailurePolicy = FailureOptional
	if _, err := NewPortPlan(PortPlan{
		Port:     PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
		Bindings: []PortBinding{optional},
	}); err == nil {
		t.Fatal("OPTIONAL dynamic context Binding was accepted")
	}
}

func TestExecutionClassRecognizesFutureProtocolClasses(t *testing.T) {
	for _, class := range []ExecutionClass{
		ExecutionDeclarative,
		ExecutionTrustedInProcess,
		ExecutionLocalProcess,
		ExecutionRemote,
		ExecutionWASM,
	} {
		if err := class.Validate(); err != nil {
			t.Fatalf("%q: %v", class, err)
		}
	}
}

func TestPortRefCanonicalKeyIsValidatedAndUnambiguous(t *testing.T) {
	ref := PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1}
	got, err := ref.CanonicalKey()
	if err != nil {
		t.Fatal(err)
	}
	if want := PortNameContextProvide + "\x00" + PortVersionV1; got != want {
		t.Fatalf("CanonicalKey() = %q, want %q", got, want)
	}
}

func validPortBinding(moduleID, instanceID string, failurePolicy FailurePolicy) PortBinding {
	return PortBinding{
		Provider: ActivatedModuleRef{
			ModuleID:           moduleID,
			Version:            "1",
			ArtifactDigest:     strings.Repeat("a", SHA256HexLength),
			InstanceID:         instanceID,
			ExecutionClass:     ExecutionDeclarative,
			AdapterIdentity:    "builtin.static-context",
			ActivationRevision: 1,
		},
		ConfigRef:           strings.Repeat("b", SHA256HexLength),
		AuthorityCeilingRef: strings.Repeat("c", SHA256HexLength),
		StaticContextRefs:   []string{},
		FailurePolicy:       failurePolicy,
	}
}
