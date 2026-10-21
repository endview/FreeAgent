package harness

import "testing"

func TestActiveDelegationBatchHonorsSerialAndToolSafety(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	record := collaborationRecord{
		Plan: CollaborationPlan{
			Budget: ParentBudget{MaxConcurrentChildren: 2},
			Roles: map[string]RoleProfile{
				"read":  {Tools: []string{"file_read"}, Paths: []string{"."}},
				"write": {Tools: []string{"file_create"}, Paths: []string{"."}},
				"text":  {},
			},
			Children: []ChildSpec{
				{Name: "read-one", Role: "read"},
				{Name: "read-two", Role: "read"},
			},
			Parallel: true,
		},
		Children: []childRecord{
			{Spec: ChildSpec{Name: "read-one", Role: "read"}, RunID: "one", Control: "running"},
			{Spec: ChildSpec{Name: "read-two", Role: "read"}, RunID: "two", Control: "running"},
		},
	}
	report := CollaborationReport{Children: []ChildReport{
		{Spec: record.Children[0].Spec, Run: Run{Status: "running"}},
		{Spec: record.Children[1].Spec, Run: Run{Status: "running"}},
	}}
	if got := activeDelegationBatch(service, record, report); len(got) != 2 {
		t.Fatalf("read-only parallel batch size=%d", len(got))
	}
	record.Plan.Parallel = false
	if got := activeDelegationBatch(service, record, report); len(got) != 1 {
		t.Fatalf("serial batch size=%d", len(got))
	}
	record.Plan.Parallel = true
	record.Plan.Roles["read"] = record.Plan.Roles["write"]
	if got := activeDelegationBatch(service, record, report); len(got) != 1 {
		t.Fatalf("write parallel batch size=%d", len(got))
	}
}
