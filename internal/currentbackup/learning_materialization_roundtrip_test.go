package currentbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type currentBackupWindowsTestOuterGoTmpDirStateV1 struct {
	owner   bool
	present bool
	value   string
}

var currentBackupWindowsTestOuterGoTmpDirV1 currentBackupWindowsTestOuterGoTmpDirStateV1

func configureCurrentBackupNestedGoCommandTempV1(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	state := currentBackupWindowsTestOuterGoTmpDirV1
	if !state.owner {
		t.Fatal("nested Go command has no owner-captured outer GOTMPDIR")
	}
	if !state.present || state.value == "" {
		return
	}
	command.Env = currentBackupNestedGoCommandEnvironmentV1(os.Environ(), state.value)
}

func currentBackupNestedGoCommandEnvironmentV1(
	environment []string,
	goTmpDir string,
) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, present := strings.Cut(entry, "=")
		if present && strings.EqualFold(name, "GOTMPDIR") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GOTMPDIR="+goTmpDir)
}

func TestLearningMaterializedVersionsRoundTripWithoutInstalledArtifacts(t *testing.T) {
	fixture := newLearningReviewBackupFixture(t)
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(
		ctx,
		fixture.base.base.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()

	// The fixture's APPROVED candidate is a static Skill. Materialize it at
	// the exact first terminal review revision.
	skillProposal := fixture.records["APPROVED"]
	skill, err := store.MaterializeApprovedLearningProposal(
		ctx,
		currentstore.MaterializeApprovedLearningProposalInput{
			TenantID:                 skillProposal.Proposal.TenantID,
			ProposalID:               skillProposal.ProposalID,
			ExpectedProposalRevision: skillProposal.Revision,
		},
	)
	if err != nil || !skill.Created ||
		skill.Record.Version.Kind != learningcontract.ProposalKindSkillV1 {
		t.Fatalf("materialize Skill=%+v error=%v", skill, err)
	}

	// Complete the fixture's REVIEW_PENDING Knowledge review, then materialize
	// that independent kind through the same Store API.
	knowledgeProposal := fixture.records["REVIEW_PENDING"]
	begin := beginBackupReviewerAttempt(
		t,
		store,
		fixture.manifests[knowledgeProposal.ProposalID],
		"KNOWLEDGE_MATERIALIZED",
	)
	commitBackupReviewerOutcome(
		t,
		store,
		begin,
		knowledgeProposal,
		corecontract.ModelAttemptSucceeded,
		learningcontract.ReviewDecisionApproveV1,
		nil,
		nil,
	)
	finalized, err := store.FinalizeLearningReview(
		ctx,
		currentstore.FinalizeLearningReviewInput{
			TenantID:                 knowledgeProposal.Proposal.TenantID,
			ProposalID:               knowledgeProposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	)
	if err != nil || finalized.Proposal.State != currentstore.LearningProposalApproved {
		t.Fatalf("finalize Knowledge=%+v error=%v", finalized, err)
	}
	knowledge, err := store.MaterializeApprovedLearningProposal(
		ctx,
		currentstore.MaterializeApprovedLearningProposalInput{
			TenantID:                 finalized.Proposal.Proposal.TenantID,
			ProposalID:               finalized.Proposal.ProposalID,
			ExpectedProposalRevision: finalized.Proposal.Revision,
		},
	)
	if err != nil || !knowledge.Created ||
		knowledge.Record.Version.Kind != learningcontract.ProposalKindKnowledgeV1 {
		t.Fatalf("materialize Knowledge=%+v error=%v", knowledge, err)
	}

	want := map[string]currentstore.LearningMaterializedVersionRecord{
		"Knowledge": knowledge.Record,
		"Skill":     skill.Record,
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	for name, record := range want {
		assertLearningMaterializationHasNoInstallation(
			t,
			fixture.base.base.databasePath,
			name,
			record,
		)
	}

	bundle := filepath.Join(t.TempDir(), "learning-materialized.bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.base.base.databasePath,
		fixture.base.base.artifactRoot,
		bundle,
		"learning-materialized-roundtrip-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	restoredDatabase := filepath.Join(t.TempDir(), "restored.sqlite")
	restoredArtifacts := filepath.Join(t.TempDir(), "restored-artifacts")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle: %v", err)
	}
	restored, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("reopen restored Store: %v", err)
	}
	for name, expected := range want {
		got, err := restored.GetLearningMaterializedVersion(
			ctx,
			expected.Proposal.Proposal.TenantID,
			expected.Proposal.ProposalID,
		)
		if err != nil {
			_ = restored.Close()
			t.Fatalf("GetLearningMaterializedVersion(%s): %v", name, err)
		}
		assertLearningMaterializedRecordEqual(t, name, got, expected)
		assertLearningMaterializedArtifactEqual(t, name, got, expected)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	for name, record := range want {
		assertLearningMaterializationHasNoInstallation(
			t,
			restoredDatabase,
			name,
			record,
		)
	}
	assertRestoredLearningMaterializeProductExport(t, restoredDatabase, restoredArtifacts, want)
}

func assertLearningMaterializedRecordEqual(
	t *testing.T,
	name string,
	got currentstore.LearningMaterializedVersionRecord,
	want currentstore.LearningMaterializedVersionRecord,
) {
	t.Helper()
	if got.Version != want.Version ||
		got.VersionID != want.VersionID ||
		got.ArtifactDigest != want.ArtifactDigest ||
		got.ArtifactSizeBytes != want.ArtifactSizeBytes ||
		got.ApprovalVerdictDigest != want.ApprovalVerdictDigest ||
		!got.MaterializedAt.Equal(want.MaterializedAt) ||
		!bytes.Equal(got.VersionCanonical, want.VersionCanonical) ||
		got.Proposal.ProposalID != want.Proposal.ProposalID ||
		got.Proposal.State != want.Proposal.State ||
		got.Proposal.Revision != want.Proposal.Revision ||
		!bytes.Equal(got.Proposal.ProposalCanonical, want.Proposal.ProposalCanonical) ||
		!bytes.Equal(got.Proposal.DraftCanonical, want.Proposal.DraftCanonical) {
		t.Fatalf("%s restored materialized record differs\ngot=%+v\nwant=%+v", name, got, want)
	}
}

func assertLearningMaterializedArtifactEqual(
	t *testing.T,
	name string,
	got currentstore.LearningMaterializedVersionRecord,
	want currentstore.LearningMaterializedVersionRecord,
) {
	t.Helper()
	gotArtifact, err := learningcontract.MaterializedVersionArtifactV1(
		got.VersionCanonical,
		got.VersionID,
		got.Proposal.ProposalCanonical,
		got.Proposal.DraftCanonical,
	)
	if err != nil {
		t.Fatalf("rebuild restored %s artifact: %v", name, err)
	}
	wantArtifact, err := learningcontract.MaterializedVersionArtifactV1(
		want.VersionCanonical,
		want.VersionID,
		want.Proposal.ProposalCanonical,
		want.Proposal.DraftCanonical,
	)
	if err != nil {
		t.Fatalf("rebuild source %s artifact: %v", name, err)
	}
	if gotArtifact.EntrypointPath != wantArtifact.EntrypointPath ||
		gotArtifact.ArtifactDigest != wantArtifact.ArtifactDigest ||
		gotArtifact.ArtifactSizeBytes != wantArtifact.ArtifactSizeBytes ||
		!bytes.Equal(gotArtifact.ManifestCanonical, wantArtifact.ManifestCanonical) ||
		!bytes.Equal(gotArtifact.PayloadCanonical, wantArtifact.PayloadCanonical) {
		t.Fatalf("%s reconstructed artifact differs after restore", name)
	}
}

func assertLearningMaterializationHasNoInstallation(
	t *testing.T,
	databasePath string,
	name string,
	record currentstore.LearningMaterializedVersionRecord,
) {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM module_installations
		WHERE module_id=? AND exact_version=?
	`, record.Version.Target.ID, record.Version.Target.Version).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%s Version unexpectedly has %d Installation rows", name, count)
	}
}

func assertRestoredLearningMaterializeProductExport(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	records map[string]currentstore.LearningMaterializedVersionRecord,
) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve currentbackup test source path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	binaryName := "freeagent-learning-materialize-test"
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command(goBinary, "build", "-o", binaryPath, "./cmd/freeagent")
	build.Dir = repositoryRoot
	configureCurrentBackupNestedGoCommandTempV1(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build freeagent product binary: %v\n%s", err, output)
	}

	for name, record := range records {
		handoff := filepath.Join(
			t.TempDir(),
			"restored-handoff-"+strings.ToLower(name),
		)
		first := runRestoredLearningMaterializeProduct(
			t,
			binaryPath,
			databasePath,
			artifactRoot,
			handoff,
			record,
		)
		if first.VersionCreated || !first.ExportCreated ||
			first.VersionID != record.VersionID ||
			first.ProposalRevision != record.Version.ProposalRevision {
			t.Fatalf("%s restored product export result=%+v", name, first)
		}
		artifact, err := learningcontract.MaterializedVersionArtifactV1(
			record.VersionCanonical,
			record.VersionID,
			record.Proposal.ProposalCanonical,
			record.Proposal.DraftCanonical,
		)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := os.ReadFile(filepath.Join(handoff, moduleapi.ArtifactManifestPath))
		if err != nil {
			t.Fatal(err)
		}
		payload, err := os.ReadFile(filepath.Join(
			handoff,
			filepath.FromSlash(artifact.EntrypointPath),
		))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(manifest, artifact.ManifestCanonical) ||
			!bytes.Equal(payload, artifact.PayloadCanonical) {
			t.Fatalf("%s product export bytes differ after restore", name)
		}
		retry := runRestoredLearningMaterializeProduct(
			t,
			binaryPath,
			databasePath,
			artifactRoot,
			handoff,
			record,
		)
		if retry.VersionCreated || retry.ExportCreated ||
			retry.VersionID != first.VersionID ||
			retry.MaterializedAt != first.MaterializedAt ||
			retry.ArtifactSource != first.ArtifactSource {
			t.Fatalf("%s restored product exact retry=%+v first=%+v", name, retry, first)
		}
	}
}

func TestCurrentBackupNestedGoCommandEnvironmentRestoresOnlyUniqueOuterGoTmpDirV1(
	t *testing.T,
) {
	environment := []string{
		"TEMP=protected",
		"FREEAGENT_CURRENTBACKUP_WINDOWS_TEST_TEMP_ROOT_V1=protected",
		"gotmpdir=stale-one",
		"=C:=C:\\working-directory",
		"GOTMPDIR=stale-two",
		"TMP=protected",
	}
	got := currentBackupNestedGoCommandEnvironmentV1(environment, `D:\outer-go-tmp`)
	want := []string{
		"TEMP=protected",
		"FREEAGENT_CURRENTBACKUP_WINDOWS_TEST_TEMP_ROOT_V1=protected",
		"=C:=C:\\working-directory",
		"TMP=protected",
		`GOTMPDIR=D:\outer-go-tmp`,
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("nested Go command environment = %q, want %q", got, want)
	}
}

type restoredLearningMaterializeProductResult struct {
	ProposalRevision uint64 `json:"proposal_revision"`
	VersionID        string `json:"version_id"`
	MaterializedAt   string `json:"materialized_at"`
	ArtifactSource   string `json:"artifact_source"`
	VersionCreated   bool   `json:"version_created"`
	ExportCreated    bool   `json:"export_created"`
}

func runRestoredLearningMaterializeProduct(
	t *testing.T,
	binaryPath string,
	databasePath string,
	artifactRoot string,
	handoff string,
	record currentstore.LearningMaterializedVersionRecord,
) restoredLearningMaterializeProductResult {
	t.Helper()
	command := exec.Command(
		binaryPath,
		"learning-materialize",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", record.Proposal.Proposal.TenantID,
		"--proposal", record.Proposal.ProposalID,
		"--proposal-revision", fmt.Sprintf("%d", record.Version.ProposalRevision),
		"--output-artifact", handoff,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run restored learning-materialize: %v\n%s", err, output)
	}
	var result restoredLearningMaterializeProductResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode restored learning-materialize output: %v\n%s", err, output)
	}
	return result
}
