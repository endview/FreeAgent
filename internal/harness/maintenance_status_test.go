package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaintenanceStatusKeepsUncertainEvidenceSeparate(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	run := reviewBudgetRun(t, service, "maintenance")
	if _, err := service.Store.db.ExecContext(t.Context(), `UPDATE fa.runs SET status='waiting_confirmation' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.db.ExecContext(t.Context(), `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES('maintenance-unknown',$1,'model','','unknown','{}','')`, run.ID); err != nil {
		t.Fatal(err)
	}
	status, err := service.Store.MaintenanceStatus(t.Context())
	if err != nil || status.WaitingConfirmations != 1 || status.UnknownAttempts != 1 || status.UnknownRuns != 0 || status.RecentFailedRuns != 0 {
		t.Fatalf("maintenance status %+v %v", status, err)
	}
	saved, err := service.Store.GetRun(t.Context(), run.ID)
	if err != nil || saved.Status != "waiting_confirmation" {
		t.Fatal("observation changed the Run", err)
	}
}

func TestControlMaintenanceDoesNotExpireProposalsAndChecksIntegrity(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	control, err := service.Control()
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetPermission, Authorization: ControlUserDirect, Arguments: mustJSON(map[string]string{"mode": "ask"})})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(service.Store.DataDir(), controlJournalName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pending, unresolved, err := ControlMaintenance(service.Store.DataDir())
	if err != nil || pending != 1 || unresolved != 0 {
		t.Fatalf("pending=%d unresolved=%d err=%v", pending, unresolved, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("read-only check changed the journal")
	}
	// A malformed or edited journal must prevent a ready-to-update claim.
	if err := os.WriteFile(path, []byte(`{"record":{"operation":{"operation_id":"x","status":"applied"}},"hash":"forged"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ControlMaintenance(service.Store.DataDir()); err == nil {
		t.Fatal("forged control journal accepted")
	}
}
