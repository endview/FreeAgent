package moduleapi

import "testing"

func TestPermissionKnowledgeReadV1IsExactValidRequest(t *testing.T) {
	if PermissionKnowledgeReadV1 != Permission("knowledge.read") {
		t.Fatalf("PermissionKnowledgeReadV1=%q", PermissionKnowledgeReadV1)
	}
	if err := PermissionKnowledgeReadV1.Validate(); err != nil {
		t.Fatalf("PermissionKnowledgeReadV1.Validate() error=%v", err)
	}
}
