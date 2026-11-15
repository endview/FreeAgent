package harness

import (
	"encoding/json"
	"testing"
)

func TestExtensionChoicesSurviveNonStringDefaults(t *testing.T) {
	manifest := ExtensionManifest{ModelSettings: []string{"mode"}, SettingsSchema: json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean","default":false},"limit":{"type":"integer","default":3},"mode":{"type":"string","default":"single","enum":["single","paragraphs"]}}}`)}
	fields := extensionTurnSettings(manifest)
	if len(fields) != 1 || fields[0].Default == nil || *fields[0].Default != "single" || fields[0].Inherited == nil || len(fields[0].Options) != 2 {
		t.Fatal(fields)
	}
}
