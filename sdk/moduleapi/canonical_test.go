package moduleapi

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestCanonicalJSONUsesRFC8785(t *testing.T) {
	input := []byte(`{
	  "numbers":[333333333.33333329,1E30,4.50,2e-3,0.000000000000000000000000001],
	  "string":"\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
	  "literals":[null,true,false]
	}`)

	got, err := CanonicalJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€$\u000f\nA'B\"\\\\\"/"}`
	if string(got) != want {
		t.Fatalf("canonical=%s, want %s", got, want)
	}
}

func TestCanonicalTextUsesNFC(t *testing.T) {
	if got, want := CanonicalText("Cafe\u0301"), "Café"; got != want {
		t.Fatalf("CanonicalText() = %q, want %q", got, want)
	}
}

func TestDigestSeparatesDomains(t *testing.T) {
	canonical := []byte(`{"a":1}`)
	got := Digest("freeagent.test.v1", canonical)

	digest := sha256.New()
	digest.Write([]byte("freeagent.test.v1"))
	digest.Write([]byte{0})
	digest.Write(canonical)
	want := hex.EncodeToString(digest.Sum(nil))
	if got != want {
		t.Fatalf("Digest() = %q, want %q", got, want)
	}
	if got == Digest("freeagent.other.v1", canonical) {
		t.Fatal("different domains produced the same digest")
	}
}

func TestCanonicalJSONRejectsLoneUTF16Surrogate(t *testing.T) {
	if _, err := CanonicalJSON([]byte(`{"bad":"\ud800"}`)); err == nil {
		t.Fatal("CanonicalJSON accepted a lone UTF-16 surrogate")
	}
}

func TestCanonicalJSONSortsObjectNamesByUTF16CodeUnits(t *testing.T) {
	got, err := CanonicalJSON([]byte("{\"\ufffd\":1,\"😀\":2}"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"😀\":2,\"\ufffd\":1}"; string(got) != want {
		t.Fatalf("canonical=%s, want %s", got, want)
	}
}

func TestCanonicalJSONUsesECMAScriptNumberThresholds(t *testing.T) {
	got, err := CanonicalJSON([]byte(`[-0,0.000001,0.0000001,100000000000000000000,1e21]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `[0,0.000001,1e-7,100000000000000000000,1e+21]`; string(got) != want {
		t.Fatalf("canonical=%s, want %s", got, want)
	}
}

func TestCanonicalJSONMatchesOfficialRFC8785NumberCorpus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"0", "0"},
		{"-0", "0"},
		{"5e-324", "5e-324"},
		{"-5e-324", "-5e-324"},
		{"1.7976931348623157e+308", "1.7976931348623157e+308"},
		{"-1.7976931348623157e+308", "-1.7976931348623157e+308"},
		{"9007199254740992", "9007199254740992"},
		{"-9007199254740992", "-9007199254740992"},
		{"295147905179352830000", "295147905179352830000"},
		{"9.999999999999997e+22", "9.999999999999997e+22"},
		{"1e+23", "1e+23"},
		{"1.0000000000000001e+23", "1.0000000000000001e+23"},
		{"999999999999999700000", "999999999999999700000"},
		{"999999999999999900000", "999999999999999900000"},
		{"1e+21", "1e+21"},
		{"9.999999999999997e-7", "9.999999999999997e-7"},
		{"0.000001", "0.000001"},
		{"333333333.3333332", "333333333.3333332"},
		{"333333333.33333325", "333333333.33333325"},
		{"333333333.3333333", "333333333.3333333"},
		{"333333333.3333334", "333333333.3333334"},
		{"333333333.33333343", "333333333.33333343"},
		{"-0.0000033333333333333333", "-0.0000033333333333333333"},
		{"1424953923781206.25", "1424953923781206.2"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := CanonicalJSON([]byte(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("canonical=%s, want %s", got, test.want)
			}
		})
	}
}

func TestCanonicalJSONRejectsBinary64RangeOverflowInsteadOfClamping(t *testing.T) {
	for _, input := range []string{
		`1e400`,
		`-1e400`,
		`1e1000`,
		`-1e1000`,
		`-123456789.987654321E+0123456789`,
	} {
		t.Run(input, func(t *testing.T) {
			if canonical, err := CanonicalJSON([]byte(input)); err == nil {
				t.Fatalf("out-of-range number canonicalized to %s", canonical)
			}
		})
	}
}

func TestCanonicalJSONKeepsFiniteUnderflowBehavior(t *testing.T) {
	const input = `[1e-1000,-1e-1000,0e12456789]`
	const want = `[0,0,0]`
	got, err := CanonicalJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("canonical=%s, want %s", got, want)
	}
}

func TestCanonicalJSONMatchesPinnedJSONTextDepthBoundary(t *testing.T) {
	for _, depth := range []int{1001, 10000} {
		payload := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		got, err := CanonicalJSON([]byte(payload))
		if err != nil {
			t.Fatalf("depth %d rejected: %v", depth, err)
		}
		if string(got) != payload {
			t.Fatalf("depth %d canonical output changed", depth)
		}
	}
	tooDeep := strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)
	if _, err := CanonicalJSON([]byte(tooDeep)); err == nil {
		t.Fatal("depth 10001 was accepted")
	}
}

func TestCanonicalJSONWithLimitsPreservesRFC8785AndEnforcesBounds(t *testing.T) {
	input := []byte(`{"z":[true,{"b":2,"a":1}],"a":"value"}`)
	want, err := CanonicalJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalJSONWithLimits(input, CanonicalJSONLimits{
		MaxBytes: len(input),
		MaxDepth: 3,
		MaxNodes: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("bounded canonical=%s, want %s", got, want)
	}

	for name, limits := range map[string]CanonicalJSONLimits{
		"bytes": {MaxBytes: len(input) - 1, MaxDepth: 3, MaxNodes: 7},
		"depth": {MaxBytes: len(input), MaxDepth: 2, MaxNodes: 7},
		"nodes": {MaxBytes: len(input), MaxDepth: 3, MaxNodes: 6},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalJSONWithLimits(input, limits); err == nil {
				t.Fatalf("%s limit was not enforced", name)
			}
		})
	}
}

func TestCanonicalJSONWithLimitsRejectsInvalidLimitsAndKeepsStrictParsing(t *testing.T) {
	valid := CanonicalJSONLimits{MaxBytes: 1024, MaxDepth: 64, MaxNodes: 1024}
	for name, limits := range map[string]CanonicalJSONLimits{
		"bytes": {MaxBytes: 0, MaxDepth: 1, MaxNodes: 1},
		"depth": {MaxBytes: 1, MaxDepth: 0, MaxNodes: 1},
		"nodes": {MaxBytes: 1, MaxDepth: 1, MaxNodes: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalJSONWithLimits([]byte(`0`), limits); err == nil {
				t.Fatalf("nonpositive %s limit was accepted", name)
			}
		})
	}
	for name, payload := range map[string][]byte{
		"duplicate":     []byte(`{"a":1,"a":2}`),
		"trailing":      []byte(`{} {}`),
		"invalid UTF-8": {0xff},
		"surrogate":     []byte(`{"bad":"\ud800"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalJSONWithLimits(payload, valid); err == nil {
				t.Fatalf("%s input was accepted", name)
			}
		})
	}
}
