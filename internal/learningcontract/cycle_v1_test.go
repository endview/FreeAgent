package learningcontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const cycleFirstDueV1 = int64(1_800_000_000_000_000)

func TestLearningCycleScheduleV1CanonicalDefaultAndDefensiveRestore(t *testing.T) {
	input := cycleScheduleInputV1(ProposalKindKnowledgeV1)
	input.IntervalSeconds = 0
	input.KnowledgeVisibility = []moduleapi.KnowledgeScopeRuleV1{
		{TenantID: "tenant-a", WorkspaceID: "workspace-z", AgentID: "*", TaskInputRef: "*"},
		{TenantID: "tenant-a", WorkspaceID: "*", AgentID: "agent-a", TaskInputRef: "*"},
	}
	frozen, canonical, digest, err := NewLearningCycleScheduleV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.IntervalSeconds != DefaultLearningCycleIntervalSecondsV1 {
		t.Fatalf("default interval = %d", frozen.IntervalSeconds)
	}
	if frozen.KnowledgeVisibility[0].WorkspaceID != "*" {
		t.Fatalf("visibility was not frozen in stable order: %+v", frozen.KnowledgeVisibility)
	}
	const expected = "50d057d7778394c7a4da51148a1749625ae6c7fc8a04af9fa170aa33d1ab7b24"
	if digest != expected {
		t.Fatalf("schedule digest canary changed: got %s want %s", digest, expected)
	}
	restored, err := RestoreLearningCycleScheduleV1(canonical, digest)
	if err != nil {
		t.Fatal(err)
	}
	canonicalBefore := bytes.Clone(canonical)
	input.KnowledgeVisibility[0].WorkspaceID = "mutated-input"
	frozen.KnowledgeVisibility[0].WorkspaceID = "mutated-return"
	restored.KnowledgeVisibility[0].WorkspaceID = "mutated-restored"
	canonical[0] ^= 1
	if !bytes.Equal(canonicalBefore, mustCycleScheduleCanonicalV1(t)) {
		t.Fatal("caller mutation changed independently rebuilt canonical schedule")
	}
}

func TestLearningCycleRequestV1DerivesExactWindowTargetAndTaskID(t *testing.T) {
	schedule, scheduleCanonical, scheduleDigest := newCycleScheduleV1(t, ProposalKindKnowledgeV1)
	request, canonical, requestDigest, err := NewLearningCycleRequestV1(
		schedule, scheduleCanonical, scheduleDigest, cycleFirstDueV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.ScheduleDigest != scheduleDigest || request.Kind != schedule.Kind ||
		request.RequestDigest != requestDigest ||
		request.Objective != schedule.Objective || request.Instructions != LearningCycleInstructionsV1 ||
		request.OutputSchemaVersion != LearningCycleResultSchemaVersionV1 ||
		request.WindowEndMicros != cycleFirstDueV1+int64(schedule.IntervalSeconds)*1_000_000 {
		t.Fatalf("request did not derive exact Schedule policy: %+v", request)
	}
	if request.Target.ID != schedule.TargetModuleID || len(request.Target.Version) != moduleapi.MaxVersionBytes {
		t.Fatalf("invalid deterministic target: %+v", request.Target)
	}
	const expectedRequest = "34a5dfdb5ac958d49dd89553788bfac6a1127ccb2d349cceba340339a10f2e46"
	if requestDigest != expectedRequest {
		t.Fatalf("request digest canary changed: got %s want %s", requestDigest, expectedRequest)
	}
	taskID, err := DeriveLearningCycleTaskIDV1(scheduleDigest, cycleFirstDueV1, requestDigest)
	if err != nil {
		t.Fatal(err)
	}
	const expectedTask = "890f823f4db2d2662e0205d402eedc522033f4b97c68f92f7e33398a6c0933e3"
	if taskID != expectedTask {
		t.Fatalf("task ID canary changed: got %s want %s", taskID, expectedTask)
	}
	restored, err := RestoreLearningCycleRequestV1(canonical, requestDigest)
	if err != nil || restored != request {
		t.Fatalf("request restore: %+v %v", restored, err)
	}

	next, _, nextDigest, err := NewLearningCycleRequestV1(
		schedule, scheduleCanonical, scheduleDigest,
		cycleFirstDueV1+int64(schedule.IntervalSeconds)*1_000_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if next.Target == request.Target || nextDigest == requestDigest {
		t.Fatal("logical window did not change exact target and request")
	}
	if _, _, _, err := NewLearningCycleRequestV1(
		schedule, scheduleCanonical, scheduleDigest, cycleFirstDueV1+1,
	); err == nil {
		t.Fatal("accepted non-window scheduled_for")
	}
	drifted := bytes.Clone(scheduleCanonical)
	drifted[len(drifted)-2] ^= 1
	if _, _, _, err := NewLearningCycleRequestV1(
		schedule, drifted, scheduleDigest, cycleFirstDueV1,
	); err == nil {
		t.Fatal("accepted drifted Schedule canonical")
	}
	mutatedRequest := request
	mutatedRequest.Target.Version = "cycle-wrong"
	mutatedCanonical, err := canonicalJSONV1(mutatedRequest)
	if err != nil {
		t.Fatal(err)
	}
	mutatedDigest := moduleapi.Digest(learningCycleRequestDigestDomainV1, mutatedCanonical)
	if _, err := RestoreLearningCycleRequestV1(mutatedCanonical, mutatedDigest); err == nil {
		t.Fatal("accepted target not derived from exact Schedule window")
	}
	var requestFields map[string]any
	if err := json.Unmarshal(canonical, &requestFields); err != nil {
		t.Fatal(err)
	}
	delete(requestFields, "request_digest")
	missingDigestCanonical, err := canonicalJSONV1(requestFields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreLearningCycleRequestV1(missingDigestCanonical, requestDigest); err == nil {
		t.Fatal("accepted request with missing self digest")
	}
	requestFields["request_digest"] = digestV1("f")
	wrongSelfCanonical, err := canonicalJSONV1(requestFields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreLearningCycleRequestV1(wrongSelfCanonical, digestV1("f")); err == nil {
		t.Fatal("accepted request with wrong self digest")
	}
}

func TestLearningCycleInstructionsV1FreezeExactFiveFieldUnionShapes(t *testing.T) {
	for _, required := range []string{
		"exactly five keys and no other keys",
		`{"schema_version":"learning-cycle-result/v1","request_digest":"<REQUEST_DIGEST>","decision":"PROPOSE","knowledge_chunks":[],"skill_text":"<NON_EMPTY_SKILL_TEXT>"}`,
		`{"schema_version":"learning-cycle-result/v1","request_digest":"<REQUEST_DIGEST>","decision":"PROPOSE","knowledge_chunks":["<NON_EMPTY_KNOWLEDGE_CHUNK>"],"skill_text":""}`,
		`{"schema_version":"learning-cycle-result/v1","request_digest":"<REQUEST_DIGEST>","decision":"NO_CHANGE","knowledge_chunks":[],"skill_text":""}`,
		"never output the placeholder literally",
	} {
		if !strings.Contains(LearningCycleInstructionsV1, required) {
			t.Fatalf("Learning instructions omitted frozen result shape %q", required)
		}
	}
}

func TestLearningCycleResultV1UnionParseBindingAndDefensiveCopy(t *testing.T) {
	requestDigest := digestV1("a")
	cases := []LearningCycleResultV1{
		{SchemaVersion: LearningCycleResultSchemaVersionV1, RequestDigest: requestDigest, Decision: LearningCycleResultProposeV1, KnowledgeChunks: []string{"first", "second"}},
		{SchemaVersion: LearningCycleResultSchemaVersionV1, RequestDigest: requestDigest, Decision: LearningCycleResultProposeV1, KnowledgeChunks: []string{}, SkillText: "A bounded static skill."},
		{SchemaVersion: LearningCycleResultSchemaVersionV1, RequestDigest: requestDigest, Decision: LearningCycleResultNoChangeV1, KnowledgeChunks: []string{}},
	}
	for index, input := range cases {
		frozen, canonical, digest, err := NewLearningCycleResultV1(input)
		if err != nil {
			t.Fatalf("case %d: %v", index, err)
		}
		if index == 0 {
			const expected = "38a43c6c4cdaca837d87b503f927a60efced1f4a82431dff68bdbf11579584f7"
			if digest != expected {
				t.Fatalf("result digest canary changed: got %s want %s", digest, expected)
			}
		}
		restored, err := RestoreLearningCycleResultV1(canonical, digest)
		if err != nil || restored.Decision != frozen.Decision {
			t.Fatalf("case %d restore: %+v %v", index, restored, err)
		}
		unordered := []byte(`{"skill_text":"` + input.SkillText + `","request_digest":"` + requestDigest + `","knowledge_chunks":` + mustJSONV1(t, input.KnowledgeChunks) + `,"decision":"` + string(input.Decision) + `","schema_version":"learning-cycle-result/v1"}`)
		parsed, parsedCanonical, parsedDigest, err := ParseLearningCycleResultV1(unordered)
		if err != nil || parsedDigest != digest || !bytes.Equal(parsedCanonical, canonical) || parsed.Decision != input.Decision {
			t.Fatalf("case %d parse: %+v %s %v", index, parsed, parsedDigest, err)
		}
	}

	knowledge := cases[0]
	frozen, canonical, _, err := NewLearningCycleResultV1(knowledge)
	if err != nil {
		t.Fatal(err)
	}
	knowledge.KnowledgeChunks[0] = "mutated-input"
	frozen.KnowledgeChunks[0] = "mutated-return"
	if strings.Contains(string(canonical), "mutated") {
		t.Fatal("caller mutation changed result canonical")
	}

	invalid := []LearningCycleResultV1{
		{SchemaVersion: LearningCycleResultSchemaVersionV1, RequestDigest: requestDigest, Decision: LearningCycleResultProposeV1},
		{SchemaVersion: LearningCycleResultSchemaVersionV1, RequestDigest: requestDigest, Decision: LearningCycleResultProposeV1, KnowledgeChunks: []string{"x"}, SkillText: "y"},
		{SchemaVersion: LearningCycleResultSchemaVersionV1, RequestDigest: requestDigest, Decision: LearningCycleResultNoChangeV1, KnowledgeChunks: []string{"x"}},
	}
	for index, value := range invalid {
		if _, _, _, err := NewLearningCycleResultV1(value); err == nil {
			t.Fatalf("accepted invalid result union %d", index)
		}
	}

	schedule, scheduleCanonical, scheduleDigest := newCycleScheduleV1(t, ProposalKindKnowledgeV1)
	request, _, exactRequestDigest, err := NewLearningCycleRequestV1(schedule, scheduleCanonical, scheduleDigest, cycleFirstDueV1)
	if err != nil {
		t.Fatal(err)
	}
	bound := cases[0]
	bound.RequestDigest = exactRequestDigest
	if err := bound.ValidateForRequestV1(request, exactRequestDigest); err != nil {
		t.Fatal(err)
	}
	driftedRequest := request
	driftedRequest.Objective = "A different but otherwise valid objective."
	if err := bound.ValidateForRequestV1(driftedRequest, exactRequestDigest); err == nil {
		t.Fatal("accepted claimed digest for a different Request value")
	}
	bound.RequestDigest = digestV1("b")
	if err := bound.ValidateForRequestV1(request, exactRequestDigest); err == nil {
		t.Fatal("accepted request digest drift")
	}
}

func TestLearningCycleReportV1SortsRejectsDuplicateWindowAndHasNoGeneratedAt(t *testing.T) {
	_, _, scheduleDigest := newCycleScheduleV1(t, ProposalKindSkillV1)
	input := LearningCycleReportV1{
		SchemaVersion: LearningCycleReportSchemaVersionV1,
		TenantID:      "tenant-a", ScheduleID: "schedule-a", ScheduleDigest: scheduleDigest,
		WindowStartMicros: cycleFirstDueV1, WindowEndMicros: cycleFirstDueV1 + 3_000,
		Entries: []LearningCycleReportEntryV1{
			{ScheduledForMicros: cycleFirstDueV1 + 2_000, TaskID: digestV1("2"), RunID: "run-2", State: LearningCycleTaskNoChangeV1},
			{ScheduledForMicros: cycleFirstDueV1 + 1_000, TaskID: digestV1("1"), State: LearningCycleTaskPendingV1},
		},
	}
	frozen, canonical, digest, err := NewLearningCycleReportV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Entries[0].TaskID != digestV1("1") || bytes.Contains(canonical, []byte("generated_at")) {
		t.Fatalf("report was not stable or contains generation time: %s", canonical)
	}
	const expected = "5fff3281252f7cefe099b2a6498582e299c6aaceb3db0520357e53fbf57a7a7c"
	if digest != expected {
		t.Fatalf("report digest canary changed: got %s want %s", digest, expected)
	}
	restored, err := RestoreLearningCycleReportV1(canonical, digest)
	if err != nil || len(restored.Entries) != 2 {
		t.Fatalf("report restore: %+v %v", restored, err)
	}
	frozen.Entries[0].RunID = "mutated"
	if bytes.Contains(canonical, []byte("mutated")) {
		t.Fatal("report canonical aliases returned entries")
	}

	duplicate := input
	duplicate.Entries[1].ScheduledForMicros = duplicate.Entries[0].ScheduledForMicros
	if _, _, _, err := NewLearningCycleReportV1(duplicate); err == nil {
		t.Fatal("accepted duplicate logical window")
	}
}

func TestLearningCycleReportV1StateReferenceShapes(t *testing.T) {
	_, _, scheduleDigest := newCycleScheduleV1(t, ProposalKindKnowledgeV1)
	base := LearningCycleReportV1{
		SchemaVersion: LearningCycleReportSchemaVersionV1,
		TenantID:      "tenant-a", ScheduleID: "schedule-a", ScheduleDigest: scheduleDigest,
		WindowStartMicros: cycleFirstDueV1, WindowEndMicros: cycleFirstDueV1 + 10,
	}
	tests := []struct {
		name     string
		state    LearningCycleTaskStateV1
		runID    string
		proposal string
		valid    bool
	}{
		{"pending", LearningCycleTaskPendingV1, "", "", true},
		{"pending with run", LearningCycleTaskPendingV1, "run-a", "", false},
		{"admitted", LearningCycleTaskRunAdmittedV1, "run-a", "", true},
		{"admitted with proposal", LearningCycleTaskRunAdmittedV1, "run-a", digestV1("a"), false},
		{"submitted", LearningCycleTaskProposalSubmittedV1, "run-a", digestV1("a"), true},
		{"source occupied", LearningCycleTaskSourceOccupiedV1, "run-a", digestV1("a"), true},
		{"content occupied", LearningCycleTaskContentOccupiedV1, "run-a", digestV1("a"), true},
		{"target occupied", LearningCycleTaskTargetOccupiedV1, "run-a", digestV1("a"), true},
		{"occupied missing proposal", LearningCycleTaskTargetOccupiedV1, "run-a", "", false},
		{"no change", LearningCycleTaskNoChangeV1, "run-a", "", true},
		{"failed", LearningCycleTaskFailedV1, "run-a", "", true},
		{"invalid", LearningCycleTaskInvalidResultV1, "run-a", "", true},
		{"unknown", LearningCycleTaskUnknownV1, "run-a", "", true},
		{"terminal with proposal", LearningCycleTaskUnknownV1, "run-a", digestV1("a"), false},
		{"terminal missing run", LearningCycleTaskFailedV1, "", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.Entries = []LearningCycleReportEntryV1{{
				ScheduledForMicros: cycleFirstDueV1, TaskID: digestV1("1"),
				RunID: test.runID, State: test.state, ProposalID: test.proposal,
			}}
			_, _, _, err := NewLearningCycleReportV1(input)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
	zeroWindow := base
	zeroWindow.WindowStartMicros = 0
	zeroWindow.Entries = []LearningCycleReportEntryV1{{
		ScheduledForMicros: 0,
		TaskID:             digestV1("1"),
		State:              LearningCycleTaskPendingV1,
	}}
	if _, _, _, err := NewLearningCycleReportV1(zeroWindow); err == nil {
		t.Fatal("accepted zero logical task window")
	}
}

func TestLearningCycleStrictWireAndBounds(t *testing.T) {
	_, scheduleCanonical, scheduleDigest := newCycleScheduleV1(t, ProposalKindKnowledgeV1)
	mutations := []struct {
		name      string
		canonical []byte
		digest    string
	}{
		{"non canonical", append([]byte(" "), scheduleCanonical...), scheduleDigest},
		{"second value", append(bytes.Clone(scheduleCanonical), []byte(`{}`)...), scheduleDigest},
		{"wrong digest", scheduleCanonical, digestV1("f")},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RestoreLearningCycleScheduleV1(test.canonical, test.digest); err == nil {
				t.Fatal("accepted invalid schedule wire")
			}
		})
	}

	var fields map[string]any
	if err := json.Unmarshal(scheduleCanonical, &fields); err != nil {
		t.Fatal(err)
	}
	fields["authority"] = "self-granted"
	unknown, err := canonicalJSONV1(fields)
	if err != nil {
		t.Fatal(err)
	}
	unknownDigest := moduleapi.Digest(learningCycleScheduleDigestDomainV1, unknown)
	if _, err := RestoreLearningCycleScheduleV1(unknown, unknownDigest); err == nil {
		t.Fatal("accepted forbidden/unknown authority field")
	}

	badSchedules := []LearningCycleScheduleV1{
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindKnowledgeV1)
			v.Objective = " padded "
			return v
		}(),
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindKnowledgeV1)
			v.Objective = "bad\x00control"
			return v
		}(),
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindKnowledgeV1)
			v.Objective = "e\u0301"
			return v
		}(),
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindKnowledgeV1)
			v.KnowledgeVisibility[0].TenantID = "tenant-b"
			return v
		}(),
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindKnowledgeV1)
			v.KnowledgeVisibility = append(v.KnowledgeVisibility, v.KnowledgeVisibility[0])
			return v
		}(),
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindSkillV1)
			v.KnowledgeVisibility = cycleScheduleInputV1(ProposalKindKnowledgeV1).KnowledgeVisibility
			return v
		}(),
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindKnowledgeV1)
			v.FirstDueAtUnixMicros = int64(^uint64(0)>>1) - 1
			return v
		}(),
		func() LearningCycleScheduleV1 {
			v := cycleScheduleInputV1(ProposalKindKnowledgeV1)
			v.FirstDueAtUnixMicros = 253_402_300_800_000_000 // 10000-01-01T00:00:00Z.
			return v
		}(),
	}
	for index, value := range badSchedules {
		if _, _, _, err := NewLearningCycleScheduleV1(value); err == nil {
			t.Fatalf("accepted invalid Schedule %d", index)
		}
	}

	unknownFieldName := "sec" + "ret"
	badResultJSON := []byte(`{"schema_version":"learning-cycle-result/v1","request_digest":"` + digestV1("a") + `","decision":"NO_CHANGE","knowledge_chunks":[],"skill_text":"","` + unknownFieldName + `":"x"}`)
	if _, _, _, err := ParseLearningCycleResultV1(badResultJSON); err == nil {
		t.Fatal("accepted unknown Secret result field")
	}
}

func TestLearningCycleAllWiresRejectUnknownSecondNonCanonicalSchemaAndDigest(t *testing.T) {
	schedule, scheduleCanonical, scheduleDigest := newCycleScheduleV1(t, ProposalKindKnowledgeV1)
	_, requestCanonical, requestDigest, err := NewLearningCycleRequestV1(
		schedule, scheduleCanonical, scheduleDigest, cycleFirstDueV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, resultCanonical, resultDigest, err := NewLearningCycleResultV1(LearningCycleResultV1{
		SchemaVersion:   LearningCycleResultSchemaVersionV1,
		RequestDigest:   requestDigest,
		Decision:        LearningCycleResultNoChangeV1,
		KnowledgeChunks: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, reportCanonical, reportDigest, err := NewLearningCycleReportV1(LearningCycleReportV1{
		SchemaVersion: LearningCycleReportSchemaVersionV1,
		TenantID:      "tenant-a", ScheduleID: schedule.ScheduleID, ScheduleDigest: scheduleDigest,
		WindowStartMicros: cycleFirstDueV1, WindowEndMicros: cycleFirstDueV1 + 1,
		Entries: []LearningCycleReportEntryV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	wires := []struct {
		name      string
		canonical []byte
		digest    string
		domain    string
		restore   func([]byte, string) error
	}{
		{"schedule", scheduleCanonical, scheduleDigest, learningCycleScheduleDigestDomainV1, func(value []byte, digest string) error {
			_, err := RestoreLearningCycleScheduleV1(value, digest)
			return err
		}},
		{"request", requestCanonical, requestDigest, learningCycleRequestDigestDomainV1, func(value []byte, digest string) error {
			_, err := RestoreLearningCycleRequestV1(value, digest)
			return err
		}},
		{"result", resultCanonical, resultDigest, learningCycleResultDigestDomainV1, func(value []byte, digest string) error {
			_, err := RestoreLearningCycleResultV1(value, digest)
			return err
		}},
		{"report", reportCanonical, reportDigest, learningCycleReportDigestDomainV1, func(value []byte, digest string) error {
			_, err := RestoreLearningCycleReportV1(value, digest)
			return err
		}},
	}
	unknownFieldName := "sec" + "ret"
	for _, wire := range wires {
		t.Run(wire.name, func(t *testing.T) {
			if err := wire.restore(append([]byte(" "), wire.canonical...), wire.digest); err == nil {
				t.Fatal("accepted non-canonical whitespace")
			}
			if err := wire.restore(append(bytes.Clone(wire.canonical), []byte(`{}`)...), wire.digest); err == nil {
				t.Fatal("accepted a second JSON value")
			}
			if err := wire.restore(wire.canonical, digestV1("f")); err == nil {
				t.Fatal("accepted wrong expected digest")
			}
			var fields map[string]any
			if err := json.Unmarshal(wire.canonical, &fields); err != nil {
				t.Fatal(err)
			}
			fields[unknownFieldName] = "forbidden"
			unknown, err := canonicalJSONV1(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := wire.restore(unknown, moduleapi.Digest(wire.domain, unknown)); err == nil {
				t.Fatal("accepted unknown Secret field")
			}
			delete(fields, unknownFieldName)
			fields["schema_version"] = "wrong/v1"
			wrongSchema, err := canonicalJSONV1(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := wire.restore(wrongSchema, moduleapi.Digest(wire.domain, wrongSchema)); err == nil {
				t.Fatal("accepted wrong schema version")
			}
		})
	}
}

func TestLearningCycleResultV1TextBoundsAndCanonicalForm(t *testing.T) {
	base := LearningCycleResultV1{
		SchemaVersion: LearningCycleResultSchemaVersionV1,
		RequestDigest: digestV1("a"), Decision: LearningCycleResultProposeV1,
	}
	invalid := []LearningCycleResultV1{
		func() LearningCycleResultV1 { v := base; v.KnowledgeChunks = []string{" padded "}; return v }(),
		func() LearningCycleResultV1 { v := base; v.KnowledgeChunks = []string{"e\u0301"}; return v }(),
		func() LearningCycleResultV1 { v := base; v.KnowledgeChunks = []string{"bad\x00control"}; return v }(),
		func() LearningCycleResultV1 {
			v := base
			v.KnowledgeChunks = []string{strings.Repeat("x", moduleapi.MaxKnowledgeHitTextBytesV1+1)}
			return v
		}(),
		func() LearningCycleResultV1 {
			v := base
			v.SkillText = strings.Repeat("x", MaxLearningCycleSkillTextBytesV1+1)
			return v
		}(),
	}
	for index, input := range invalid {
		if _, _, _, err := NewLearningCycleResultV1(input); err == nil {
			t.Fatalf("accepted invalid result text %d", index)
		}
	}
}

func TestLearningCycleResultV1RequiresEveryExplicitWireField(t *testing.T) {
	valid := map[string]any{
		"schema_version":   LearningCycleResultSchemaVersionV1,
		"request_digest":   digestV1("a"),
		"decision":         LearningCycleResultNoChangeV1,
		"knowledge_chunks": []string{},
		"skill_text":       "",
	}
	for _, name := range []string{
		"schema_version", "request_digest", "decision", "knowledge_chunks", "skill_text",
	} {
		input := make(map[string]any, len(valid))
		for key, value := range valid {
			input[key] = value
		}
		delete(input, name)
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := ParseLearningCycleResultV1(encoded); err == nil {
			t.Fatalf("accepted result missing %s", name)
		}
	}
	for _, test := range []struct {
		name  string
		value any
	}{
		{"null chunks", nil},
		{"object chunks", map[string]any{}},
		{"string chunks", ""},
	} {
		input := make(map[string]any, len(valid))
		for key, value := range valid {
			input[key] = value
		}
		input["knowledge_chunks"] = test.value
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := ParseLearningCycleResultV1(encoded); err == nil {
			t.Fatalf("accepted %s", test.name)
		}
	}
	input := make(map[string]any, len(valid))
	for key, value := range valid {
		input[key] = value
	}
	input["skill_text"] = nil
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ParseLearningCycleResultV1(encoded); err == nil {
		t.Fatal("accepted null skill_text")
	}
}

func TestLearningCycleUnixMicrosStayWithinJSONSafeInteger(t *testing.T) {
	maximumFirstDue := MaxLearningCycleUnixMicrosV1 -
		int64(DefaultLearningCycleIntervalSecondsV1*1_000_000)
	scheduleInput := cycleScheduleInputV1(ProposalKindKnowledgeV1)
	scheduleInput.FirstDueAtUnixMicros = maximumFirstDue
	schedule, canonical, digest, err := NewLearningCycleScheduleV1(scheduleInput)
	if err != nil {
		t.Fatalf("max safe Schedule: %v", err)
	}
	request, _, _, err := NewLearningCycleRequestV1(
		schedule,
		canonical,
		digest,
		maximumFirstDue,
	)
	if err != nil || request.WindowEndMicros != MaxLearningCycleUnixMicrosV1 {
		t.Fatalf("max safe Request window: %+v %v", request, err)
	}

	over := MaxLearningCycleUnixMicrosV1 + 1
	scheduleInput = cycleScheduleInputV1(ProposalKindKnowledgeV1)
	scheduleInput.FirstDueAtUnixMicros = over
	if _, _, _, err := NewLearningCycleScheduleV1(scheduleInput); err == nil {
		t.Fatal("accepted Schedule microseconds above JSON safe integer")
	}
	if _, err := DeriveLearningCycleTargetV1(digestV1("a"), over, "freeagent.learning.domain"); err == nil {
		t.Fatal("accepted target window above JSON safe integer")
	}
	if _, err := DeriveLearningCycleTaskIDV1(digestV1("a"), over, digestV1("b")); err == nil {
		t.Fatal("accepted TaskID window above JSON safe integer")
	}
	if _, _, _, err := NewLearningCycleReportV1(LearningCycleReportV1{
		SchemaVersion: LearningCycleReportSchemaVersionV1,
		TenantID:      "tenant-a", ScheduleID: "schedule-a", ScheduleDigest: digestV1("a"),
		WindowStartMicros: MaxLearningCycleUnixMicrosV1, WindowEndMicros: over,
		Entries: []LearningCycleReportEntryV1{},
	}); err == nil {
		t.Fatal("accepted Report window above JSON safe integer")
	}
}

func TestLearningCycleExecutionIdentityV1DeterministicCanaryAndBounds(t *testing.T) {
	taskID := digestV1("a")
	requestDigest := digestV1("b")
	identity, err := DeriveLearningCycleExecutionIdentityV1(taskID, requestDigest)
	if err != nil {
		t.Fatal(err)
	}
	want := LearningCycleExecutionIdentityV1{
		AdmissionKey:    "learning-cycle-admission-e45a1e0e5455003737dab0beedcedd7ba787031438baedb9e9e0c43ce839eefe",
		RunID:           "learning-cycle-run-8ff40ed300e797692110c9ec953f34440bff08dc542fdc35b11b2a13f372e8d8",
		MemberID:        "learning-cycle-member-ee6724e37ef191e39f6b865999b0c91064c81f456c187d5808b1479a7ff270f7",
		RecoveryRootRef: "learning-cycle-recovery-e17e9808af2d98f40c8252da17d341b0411612c4fb82fb7a6b5828992cfdca15",
	}
	if identity != want {
		t.Fatalf("execution identity canary changed:\n got %+v\nwant %+v", identity, want)
	}
	repeated, err := DeriveLearningCycleExecutionIdentityV1(taskID, requestDigest)
	if err != nil || repeated != identity {
		t.Fatalf("execution identity is not deterministic: %+v %v", repeated, err)
	}
	for name, value := range map[string]string{
		"admission": identity.AdmissionKey,
		"run":       identity.RunID,
		"member":    identity.MemberID,
		"recovery":  identity.RecoveryRootRef,
	} {
		if !validOpaqueV1(value) {
			t.Fatalf("%s is not a bounded canonical opaque identity: %q", name, value)
		}
	}
	changed, err := DeriveLearningCycleExecutionIdentityV1(digestV1("c"), requestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if changed.AdmissionKey == identity.AdmissionKey || changed.RunID == identity.RunID ||
		changed.MemberID == identity.MemberID || changed.RecoveryRootRef == identity.RecoveryRootRef {
		t.Fatal("changed TaskID reused an execution identity")
	}
}

func TestLearningCycleExecutionIdentityV1RejectsInvalidParents(t *testing.T) {
	valid := digestV1("a")
	for _, test := range []struct {
		name          string
		taskID        string
		requestDigest string
	}{
		{"missing TaskID", "", valid},
		{"short TaskID", strings.Repeat("a", 63), valid},
		{"uppercase TaskID", strings.Repeat("A", 64), valid},
		{"non-hex TaskID", strings.Repeat("z", 64), valid},
		{"missing Request digest", valid, ""},
		{"short Request digest", valid, strings.Repeat("b", 63)},
		{"uppercase Request digest", valid, strings.Repeat("B", 64)},
		{"non-hex Request digest", valid, strings.Repeat("z", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DeriveLearningCycleExecutionIdentityV1(test.taskID, test.requestDigest); err == nil {
				t.Fatal("accepted invalid execution identity parent")
			}
		})
	}
}

func cycleScheduleInputV1(kind ProposalKindV1) LearningCycleScheduleV1 {
	input := LearningCycleScheduleV1{
		SchemaVersion: LearningCycleScheduleSchemaVersionV1,
		TenantID:      "tenant-a", ScheduleID: "schedule-a", ServicePrincipalID: "learning-service",
		WorkspaceID: "workspace-a", AgentID: "agent-a", ProfileID: "profile-a",
		Kind: kind, TargetModuleID: "freeagent.learning.domain", Objective: "Refresh the bounded domain candidate.",
		FirstDueAtUnixMicros: cycleFirstDueV1, IntervalSeconds: DefaultLearningCycleIntervalSecondsV1,
		MaxOutputTokens: 2048,
	}
	if kind == ProposalKindKnowledgeV1 {
		input.KnowledgeVisibility = []moduleapi.KnowledgeScopeRuleV1{{
			TenantID: "tenant-a", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
		}}
	} else {
		input.KnowledgeVisibility = []moduleapi.KnowledgeScopeRuleV1{}
	}
	return input
}

func newCycleScheduleV1(t *testing.T, kind ProposalKindV1) (LearningCycleScheduleV1, []byte, string) {
	t.Helper()
	frozen, canonical, digest, err := NewLearningCycleScheduleV1(cycleScheduleInputV1(kind))
	if err != nil {
		t.Fatal(err)
	}
	return frozen, canonical, digest
}

func mustCycleScheduleCanonicalV1(t *testing.T) []byte {
	t.Helper()
	input := cycleScheduleInputV1(ProposalKindKnowledgeV1)
	input.IntervalSeconds = 0
	input.KnowledgeVisibility = []moduleapi.KnowledgeScopeRuleV1{
		{TenantID: "tenant-a", WorkspaceID: "workspace-z", AgentID: "*", TaskInputRef: "*"},
		{TenantID: "tenant-a", WorkspaceID: "*", AgentID: "agent-a", TaskInputRef: "*"},
	}
	_, canonical, _, err := NewLearningCycleScheduleV1(input)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mustJSONV1(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
