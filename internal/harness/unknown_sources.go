package harness

import "strings"

// These are persistence boundaries, not model judgements. Unknown sources
// default to blocking; only tool_response permits the allowlisted handoff.
var unknownSources = map[string]string{
	"tool outcome unknown":                                       "tool_response",
	"model outcome unknown":                                      "model_response",
	"embedding outcome unknown":                                  "embedding_response",
	"invalid or incomplete model response":                       "model_protocol",
	"output accounting stopped; result must be inspected":        "evidence_storage",
	"embedding output accounting stopped":                        "evidence_storage",
	"tool output accounting stopped; effects may have occurred":  "evidence_storage",
	"local transaction outcome unresolved; inspect saved result": "local_commit",
	"control commit outcome unknown":                             "control_commit",
	"synthesis persistence failed; no replay":                    "synthesis_commit",
}

func unknownSource(reason string) string {
	for prefix, source := range unknownSources {
		if reason == prefix || strings.HasPrefix(reason, prefix+": ") {
			return source
		}
	}
	return "unclassified"
}
