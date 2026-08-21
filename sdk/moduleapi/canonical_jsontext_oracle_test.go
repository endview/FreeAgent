//go:build goexperiment.jsonv2

package moduleapi

import (
	"encoding/json/jsontext"
	"strings"
	"testing"
)

func TestCanonicalJSONMatchesPinnedJSONTextOracle(t *testing.T) {
	inputs := []string{
		`[0,-0,0.0,-0.0,1.00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001,1e-1000,-1e-1000,-5e-324,1e+100,1.7976931348623157e+308,9007199254740990,9007199254740991,9007199254740992,9007199254740993,9007199254740994,-9223372036854775808,9223372036854775807,18446744073709551615]`,
		`[0,-0,5e-324,-5e-324,1.7976931348623157e+308,-1.7976931348623157e+308,9007199254740992,-9007199254740992,295147905179352830000,9.999999999999997e+22,1e+23,1.0000000000000001e+23,999999999999999700000,999999999999999900000,1e+21,9.999999999999997e-7,0.000001,333333333.3333332,333333333.33333325,333333333.3333333,333333333.3333334,333333333.33333343,-0.0000033333333333333333,1424953923781206.25]`,
		`{"\u20ac":"Euro Sign","\r":"Carriage Return","\ufb33":"Hebrew Letter Dalet With Dagesh","1":"One","\ud83d\ude00":"Emoji: Grinning Face","\u0080":"Control","\u00f6":"Latin Small Letter O With Diaeresis"}`,
	}
	for _, input := range inputs {
		got, err := CanonicalJSON([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		oracle := jsontext.Value(append([]byte(nil), input...))
		if err := oracle.Canonicalize(); err != nil {
			t.Fatal(err)
		}
		if string(got) != string(oracle) {
			t.Fatalf("CanonicalJSON=%s\njsontext=%s", got, oracle)
		}
	}
}

func TestCanonicalJSONRejectsRangeWhereJSONTextWouldClamp(t *testing.T) {
	for _, input := range []string{`1e400`, `-1e400`, `1e1000`, `-1e1000`} {
		oracle := jsontext.Value(input)
		if err := oracle.Canonicalize(); err != nil {
			t.Fatalf("jsontext behavior changed for %s: %v", input, err)
		}
		if _, err := CanonicalJSON([]byte(input)); err == nil {
			t.Fatalf("CanonicalJSON accepted out-of-range binary64 input %s", input)
		}
	}
}

func TestCanonicalJSONMatchesPinnedJSONTextOracleDepth(t *testing.T) {
	for _, depth := range []int{1001, 10000} {
		input := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		got, err := CanonicalJSON([]byte(input))
		if err != nil {
			t.Fatalf("CanonicalJSON depth %d: %v", depth, err)
		}
		oracle := jsontext.Value(append([]byte(nil), input...))
		if err := oracle.Canonicalize(); err != nil {
			t.Fatalf("jsontext depth %d: %v", depth, err)
		}
		if string(got) != string(oracle) {
			t.Fatalf("depth %d output differs from jsontext", depth)
		}
	}
	tooDeep := strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)
	if _, err := CanonicalJSON([]byte(tooDeep)); err == nil {
		t.Fatal("CanonicalJSON accepted depth 10001")
	}
	oracle := jsontext.Value(tooDeep)
	if err := oracle.Canonicalize(); err == nil {
		t.Fatal("jsontext accepted depth 10001")
	}
}
