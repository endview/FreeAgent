package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidConversation identifies an invalid Conversation identity or
	// caller contract. Conversation IDs and their fixed scope use the same
	// canonical opaque-ID rules as Run Admission identities.
	ErrInvalidConversation = errors.New("currentstore: invalid Conversation")

	// ErrConversationNotFound identifies an absent Conversation in the exact
	// Tenant scope requested by the caller.
	ErrConversationNotFound = errors.New("currentstore: Conversation not found")

	// ErrConversationConflict identifies reuse of one globally unique
	// Conversation ID for a different immutable scope.
	ErrConversationConflict = errors.New("currentstore: Conversation identity conflict")

	// ErrConversationIntegrity identifies a persisted Conversation row that
	// violates its immutable identity, timestamp, or head/revision closure.
	ErrConversationIntegrity = errors.New("currentstore: Conversation integrity violation")
)

// CreateConversationInput freezes the stable owner and execution identities
// of one Conversation. Exact Workspace, Agent, and Profile versions remain
// per-Run facts; only these stable IDs are fixed here.
type CreateConversationInput struct {
	ConversationID string
	TenantID       string
	PrincipalID    string
	WorkspaceID    string
	AgentID        string
	ProfileID      string
}

// ConversationRecord is the verified Current Store view of one Conversation
// head. A newly created Conversation has Revision zero and an empty HeadRunID.
type ConversationRecord struct {
	ConversationID string
	TenantID       string
	PrincipalID    string
	WorkspaceID    string
	AgentID        string
	ProfileID      string
	HeadRunID      string
	Revision       uint64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateConversationResult distinguishes a new row from an exact idempotent
// retry. Both paths return the same verified immutable record.
type CreateConversationResult struct {
	Record  ConversationRecord
	Created bool
}

// CreateConversation creates a revision-zero Conversation in one BEGIN
// IMMEDIATE transaction. Repeating the exact globally unique ID and fixed
// scope is idempotent; reusing that ID for another scope fails closed.
func (store *Store) CreateConversation(
	ctx context.Context,
	input CreateConversationInput,
) (CreateConversationResult, error) {
	if ctx == nil {
		return CreateConversationResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidConversation,
		)
	}
	if err := validateConversationInput(input); err != nil {
		return CreateConversationResult{}, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CreateConversationResult{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CreateConversationResult{}, fmt.Errorf(
			"currentstore: acquire Conversation connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CreateConversationResult{}, fmt.Errorf(
			"currentstore: begin CreateConversation: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	existing, err := queryConversation(
		ctx,
		connection,
		input.ConversationID,
	)
	if err == nil {
		if !conversationScopeMatchesInput(existing, input) {
			return CreateConversationResult{}, fmt.Errorf(
				"%w: Conversation ID %q already belongs to another scope",
				ErrConversationConflict,
				input.ConversationID,
			)
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CreateConversationResult{}, fmt.Errorf(
				"currentstore: commit idempotent Conversation create: %w",
				err,
			)
		}
		committed = true
		return CreateConversationResult{Record: existing, Created: false}, nil
	}
	if !errors.Is(err, ErrConversationNotFound) {
		return CreateConversationResult{}, err
	}

	createdAt := nowUnixMicro()
	result, err := connection.ExecContext(ctx, `
		INSERT INTO conversations(
			conversation_id,
			tenant_id,
			principal_id,
			workspace_id,
			agent_id,
			profile_id,
			head_run_id,
			revision,
			created_at,
			updated_at
		) VALUES(?, ?, ?, ?, ?, ?, NULL, 0, ?, ?)
	`,
		input.ConversationID,
		input.TenantID,
		input.PrincipalID,
		input.WorkspaceID,
		input.AgentID,
		input.ProfileID,
		createdAt,
		createdAt,
	)
	if err != nil {
		return CreateConversationResult{}, fmt.Errorf(
			"currentstore: insert Conversation: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return CreateConversationResult{}, fmt.Errorf(
			"%w: Conversation insert affected %d rows: %v",
			ErrConversationIntegrity,
			affected,
			err,
		)
	}
	record, err := queryConversation(
		ctx,
		connection,
		input.ConversationID,
	)
	if err != nil {
		return CreateConversationResult{}, err
	}
	if !conversationScopeMatchesInput(record, input) || record.Revision != 0 ||
		record.HeadRunID != "" {
		return CreateConversationResult{}, fmt.Errorf(
			"%w: created row differs from requested revision-zero scope",
			ErrConversationIntegrity,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CreateConversationResult{}, fmt.Errorf(
			"currentstore: commit CreateConversation: %w",
			err,
		)
	}
	committed = true
	return CreateConversationResult{Record: record, Created: true}, nil
}

// GetConversation returns one exact Tenant-scoped Conversation. It does not
// read Control, Catalog, optional modules, model providers, or any prompt data.
func (store *Store) GetConversation(
	ctx context.Context,
	tenantID string,
	conversationID string,
) (ConversationRecord, error) {
	if ctx == nil {
		return ConversationRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidConversation,
		)
	}
	if err := validateConversationIDs(map[string]string{
		"tenant_id":       tenantID,
		"conversation_id": conversationID,
	}); err != nil {
		return ConversationRecord{}, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return ConversationRecord{}, err
	}
	defer unlock()
	record, err := queryConversation(ctx, store.db, conversationID)
	if err != nil {
		return ConversationRecord{}, err
	}
	if record.TenantID != tenantID {
		return ConversationRecord{}, fmt.Errorf(
			"%w: tenant=%q conversation=%q",
			ErrConversationNotFound,
			tenantID,
			conversationID,
		)
	}
	return record, nil
}

type conversationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryConversation(
	ctx context.Context,
	queryer conversationQueryer,
	conversationID string,
) (ConversationRecord, error) {
	var (
		record          ConversationRecord
		headRunID       sql.NullString
		storedRevision  int64
		createdAtMicros int64
		updatedAtMicros int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			conversation_id,
			tenant_id,
			principal_id,
			workspace_id,
			agent_id,
			profile_id,
			head_run_id,
			revision,
			created_at,
			updated_at
		FROM conversations
		WHERE conversation_id=?
	`, conversationID).Scan(
		&record.ConversationID,
		&record.TenantID,
		&record.PrincipalID,
		&record.WorkspaceID,
		&record.AgentID,
		&record.ProfileID,
		&headRunID,
		&storedRevision,
		&createdAtMicros,
		&updatedAtMicros,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationRecord{}, fmt.Errorf(
			"%w: conversation=%q",
			ErrConversationNotFound,
			conversationID,
		)
	}
	if err != nil {
		return ConversationRecord{}, fmt.Errorf(
			"currentstore: read Conversation: %w",
			err,
		)
	}
	if err := validateConversationIDs(map[string]string{
		"conversation_id": record.ConversationID,
		"tenant_id":       record.TenantID,
		"principal_id":    record.PrincipalID,
		"workspace_id":    record.WorkspaceID,
		"agent_id":        record.AgentID,
		"profile_id":      record.ProfileID,
	}); err != nil {
		return ConversationRecord{}, fmt.Errorf(
			"%w: stored fixed scope is invalid: %v",
			ErrConversationIntegrity,
			err,
		)
	}
	if record.ConversationID != conversationID {
		return ConversationRecord{}, fmt.Errorf(
			"%w: stored Conversation ID differs from lookup",
			ErrConversationIntegrity,
		)
	}
	if storedRevision < 0 || uint64(storedRevision) > math.MaxInt64 {
		return ConversationRecord{}, fmt.Errorf(
			"%w: stored revision is outside the supported range",
			ErrConversationIntegrity,
		)
	}
	record.Revision = uint64(storedRevision)
	record.HeadRunID = headRunID.String
	if (record.Revision == 0 && record.HeadRunID != "") ||
		(record.Revision > 0 && !validConversationID(record.HeadRunID)) {
		return ConversationRecord{}, fmt.Errorf(
			"%w: stored head/revision closure is invalid",
			ErrConversationIntegrity,
		)
	}
	record.CreatedAt, err = timeFromUnixMicro(createdAtMicros)
	if err != nil {
		return ConversationRecord{}, fmt.Errorf(
			"%w: stored created_at is invalid",
			ErrConversationIntegrity,
		)
	}
	record.UpdatedAt, err = timeFromUnixMicro(updatedAtMicros)
	if err != nil || record.UpdatedAt.Before(record.CreatedAt) {
		return ConversationRecord{}, fmt.Errorf(
			"%w: stored updated_at is invalid",
			ErrConversationIntegrity,
		)
	}
	return record, nil
}

func validateConversationInput(input CreateConversationInput) error {
	return validateConversationIDs(map[string]string{
		"conversation_id": input.ConversationID,
		"tenant_id":       input.TenantID,
		"principal_id":    input.PrincipalID,
		"workspace_id":    input.WorkspaceID,
		"agent_id":        input.AgentID,
		"profile_id":      input.ProfileID,
	})
}

func validateConversationIDs(identities map[string]string) error {
	for name, value := range identities {
		if !validConversationID(value) {
			return fmt.Errorf(
				"%w: %s must be a canonical, trimmed UTF-8 opaque ID",
				ErrInvalidConversation,
				name,
			)
		}
	}
	return nil
}

func validConversationID(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func conversationScopeMatchesInput(
	record ConversationRecord,
	input CreateConversationInput,
) bool {
	return record.ConversationID == input.ConversationID &&
		record.TenantID == input.TenantID &&
		record.PrincipalID == input.PrincipalID &&
		record.WorkspaceID == input.WorkspaceID &&
		record.AgentID == input.AgentID &&
		record.ProfileID == input.ProfileID
}
