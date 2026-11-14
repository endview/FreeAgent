package harness

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
)

var historyReferencePattern = regexp.MustCompile(`#m[0-9]+`)

func summaryReferences(text string) []string {
	return historyReferencePattern.FindAllString(text, 129)
}

func (service *Service) validateSummaryReferences(ctx context.Context, run Run, text string, allowed []string) error {
	refs := summaryReferences(text)
	invalid := func() error {
		return &CallError{Certain: true, Category: "context", Code: "summary_incomplete", Message: "summary contains an invalid or unauthorized history reference; previous summary retained"}
	}
	if len(refs) > 128 {
		return invalid()
	}
	set := map[string]bool{}
	for _, ref := range allowed {
		set[ref] = true
	}
	for _, ref := range refs {
		id, err := contextReadID(ref)
		if err != nil || !set[ref] {
			return invalid()
		}
		var m Message
		var raw string
		err = service.Store.db.QueryRowContext(ctx, `SELECT id,coalesce(run_id,''),body_json FROM `+conversationRecords+` WHERE conversation_id=$1 AND id=$2 AND coalesce(body_json->>'Deferred','false')='false'`, run.ConversationID, id).Scan(&m.ID, &m.RunID, &raw)
		if err != nil {
			return errors.Join(invalid(), err)
		}
		if json.Unmarshal([]byte(raw), &m.ModelMessage) != nil {
			return ErrStoreFormat
		}
		scope := ""
		if service.scopedHistory() {
			scope = service.publicSummaryScope()
		}
		visible, err := contextVisible(m, scope, "", false)
		if err != nil {
			return err
		}
		if len(visible) != 1 {
			return invalid()
		}
	}
	return nil
}
