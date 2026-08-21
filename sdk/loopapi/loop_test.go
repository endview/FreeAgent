package loopapi

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type loopStub struct{}

func (loopStub) Run(
	_ context.Context,
	input RunInput,
) (RunResult, error) {
	return RunResult{
		RunID:         input.RunID,
		Disposition:   DispositionYielded,
		FrameRevision: 7,
		ReasonCode:    "step-budget-exhausted",
	}, nil
}

func TestLoopContractAndValues(t *testing.T) {
	var _ Loop = loopStub{}

	input := validRunInput()
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	result, err := (loopStub{}).Run(context.Background(), input)
	if err != nil || result.Validate() != nil {
		t.Fatalf("Run()=(%+v,%v)", result, err)
	}
	if result.RunID != input.RunID ||
		result.Disposition != DispositionYielded {
		t.Fatalf("result=%+v", result)
	}
}

func TestLoopPublicSurfaceIsMinimal(t *testing.T) {
	loopType := reflect.TypeOf((*Loop)(nil)).Elem()
	if loopType.NumMethod() != 1 {
		t.Fatalf("Loop methods=%d", loopType.NumMethod())
	}
	method := loopType.Method(0)
	if method.Name != "Run" ||
		method.Type.NumIn() != 2 ||
		method.Type.In(0) !=
			reflect.TypeOf((*context.Context)(nil)).Elem() ||
		method.Type.In(1) != reflect.TypeOf(RunInput{}) ||
		method.Type.NumOut() != 2 ||
		method.Type.Out(0) != reflect.TypeOf(RunResult{}) ||
		method.Type.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatalf("Loop.Run signature=%v", method.Type)
	}

	tests := []struct {
		value any
		want  []string
	}{
		{
			value: RunInput{},
			want:  []string{"RunID", "MaxSteps", "MaxDuration"},
		},
		{
			value: RunResult{},
			want: []string{
				"RunID",
				"Disposition",
				"FrameRevision",
				"ReasonCode",
			},
		},
	}
	for _, test := range tests {
		typ := reflect.TypeOf(test.value)
		got := make([]string, 0, typ.NumField())
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if !field.IsExported() {
				t.Fatalf("%s.%s is private", typ, field.Name)
			}
			if field.Tag.Get("json") != "" {
				t.Fatalf(
					"%s.%s unexpectedly defines a wire tag",
					typ,
					field.Name,
				)
			}
			got = append(got, field.Name)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s fields=%v want=%v", typ, got, test.want)
		}
	}
}

func TestLoopValuesRejectInvalidShape(t *testing.T) {
	valid := validRunInput()
	tests := []struct {
		name   string
		mutate func(*RunInput)
	}{
		{
			name: "run",
			mutate: func(value *RunInput) {
				value.RunID = ""
			},
		},
		{
			name: "run padded",
			mutate: func(value *RunInput) {
				value.RunID = " run-a "
			},
		},
		{
			name: "zero steps",
			mutate: func(value *RunInput) {
				value.MaxSteps = 0
			},
		},
		{
			name: "zero duration",
			mutate: func(value *RunInput) {
				value.MaxDuration = 0
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}

	for _, disposition := range []Disposition{
		DispositionYielded,
		DispositionWaitingInput,
		DispositionWaitingExternal,
		DispositionWaitingReconciliation,
		DispositionTerminated,
	} {
		result := RunResult{
			RunID:         valid.RunID,
			Disposition:   disposition,
			FrameRevision: 1,
			ReasonCode:    "test",
		}
		if result.Validate() != nil {
			t.Fatalf("valid disposition %q rejected", disposition)
		}
	}
	result := RunResult{
		RunID:       valid.RunID,
		Disposition: "RETRY",
		ReasonCode:  "test",
	}
	if result.Validate() == nil {
		t.Fatal("retry disposition accepted")
	}
}

func validRunInput() RunInput {
	return RunInput{
		RunID:       "run-a",
		MaxSteps:    4,
		MaxDuration: 3 * time.Second,
	}
}
