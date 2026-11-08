package channelwire

import (
	"strings"
	"testing"
)

func TestSplitTextConservesUnicodeAndEntityRanges(t *testing.T) {
	for _, input := range []string{strings.Repeat("😀", 5000), strings.Repeat("a\u0301", 5000), strings.Repeat("👩🏽‍💻", 2000), strings.Repeat("x", 12000), "a" + strings.Repeat("\u0301", 5000)} {
		parts := SplitTextParts(TextPart{Text: input, Entities: []TextEntity{{Type: "bold", Length: UTF16Len(input)}}}, 3500)
		var joined strings.Builder
		for _, part := range parts {
			if UTF16Len(part.Text) > 3500 || len(part.Entities) != 1 || part.Entities[0].Offset != 0 || part.Entities[0].Length != UTF16Len(part.Text) {
				t.Fatal("invalid split", len(part.Text), part.Entities)
			}
			joined.WriteString(part.Text)
		}
		if joined.String() != input {
			t.Fatal("text changed")
		}
	}
}
