package channelwire

import (
	"strings"
	"unicode/utf16"

	"github.com/rivo/uniseg"
)

// SplitTextParts keeps grapheme boundaries and entity ranges intact. An
// oversized grapheme (malformed combining-mark input) is split at rune
// boundaries as a bounded fallback; ordinary emoji/ZWJ clusters stay whole.
func SplitTextParts(part TextPart, limit int) []TextPart {
	if part.Text == "" || limit < 2 {
		return nil
	}
	var parts []TextPart
	remaining := part.Text
	base := 0
	for remaining != "" {
		g := uniseg.NewGraphemes(remaining)
		end, units, preferred := 0, 0, 0
		for g.Next() {
			_, stop := g.Positions()
			cluster := g.Str()
			n := UTF16Len(cluster)
			if units+n > limit {
				break
			}
			units += n
			end = stop
			if units >= limit/2 && (strings.HasSuffix(cluster, "\n") || strings.HasSuffix(cluster, " ") || strings.ContainsAny(cluster, "。！？.!?")) {
				preferred = stop
			}
		}
		if end == 0 {
			units = 0
			for index, r := range remaining {
				if units+utf16.RuneLen(r) > limit {
					end = index
					break
				}
				units += utf16.RuneLen(r)
			}
			if end == 0 {
				end = len(remaining)
			}
		} else if end < len(remaining) && preferred > 0 {
			end = preferred
		}
		text := remaining[:end]
		size := UTF16Len(text)
		next := TextPart{Text: text}
		for _, entity := range part.Entities {
			start := max(entity.Offset, base)
			stop := min(entity.Offset+entity.Length, base+size)
			if start < stop {
				entity.Offset, entity.Length = start-base, stop-start
				next.Entities = append(next.Entities, entity)
			}
		}
		parts = append(parts, next)
		base += size
		remaining = remaining[end:]
	}
	return parts
}

func UTF16Len(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}
