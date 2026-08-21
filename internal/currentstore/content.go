package currentstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const contentDigestDomain = "freeagent.content-record/v1\x00"

var (
	// ErrStoreClosed is returned when a Current Store method is called with a
	// nil, uninitialized, or already closed Store.
	ErrStoreClosed = errors.New("currentstore: Store is not open")

	// ErrInvalidContent identifies a malformed content kind, media type, digest,
	// or non-canonical JSON body.
	ErrInvalidContent = errors.New("currentstore: invalid content")

	// ErrContentIntegrity identifies a digest mismatch or a stored row whose
	// immutable fields differ from a proposed row with the same digest.
	ErrContentIntegrity = errors.New("currentstore: content integrity violation")

	// ErrContentNotFound distinguishes an absent digest from invalid input or a
	// storage failure.
	ErrContentNotFound = errors.New("currentstore: content not found")
)

// ContentKind is the closed set of immutable ContentRecord consumers.
type ContentKind string

const (
	ContentModuleManifest            ContentKind = "MODULE_MANIFEST"
	ContentConfig                    ContentKind = "CONFIG"
	ContentAuthorityCeiling          ContentKind = "AUTHORITY_CEILING"
	ContentTaskInput                 ContentKind = "TASK_INPUT"
	ContentPolicy                    ContentKind = "POLICY"
	ContentModelRequest              ContentKind = "MODEL_REQUEST"
	ContentContextCompilation        ContentKind = "CONTEXT_COMPILATION"
	ContentModelResult               ContentKind = "MODEL_RESULT"
	ContentActionProposal            ContentKind = "ACTION_PROPOSAL"
	ContentActionResult              ContentKind = "ACTION_RESULT"
	ContentProviderReceipt           ContentKind = "PROVIDER_RECEIPT"
	ContentStaticContext             ContentKind = "STATIC_CONTEXT"
	ContentMemorySnapshot            ContentKind = "MEMORY_SNAPSHOT"
	ContentReconciliationEvidence    ContentKind = "RECONCILIATION_EVIDENCE"
	ContentRunEventPayload           ContentKind = "RUN_EVENT_PAYLOAD"
	ContentRunCancellation           ContentKind = "RUN_CANCELLATION"
	ContentChannelCursor             ContentKind = "CHANNEL_CURSOR"
	ContentChannelIngressEnvelope    ContentKind = "CHANNEL_INGRESS_ENVELOPE"
	ContentChannelSendProposal       ContentKind = "CHANNEL_SEND_PROPOSAL"
	ContentChannelSendResult         ContentKind = "CHANNEL_SEND_RESULT"
	ContentWorkspaceTransferPayload  ContentKind = "WORKSPACE_TRANSFER_PAYLOAD"
	ContentWorkspaceTransferEnvelope ContentKind = "WORKSPACE_TRANSFER_ENVELOPE"
)

// ContentInput is the complete immutable body accepted by PutContent. Digest
// is supplied by the caller and independently recomputed by the Store.
type ContentInput struct {
	Digest         string
	Kind           ContentKind
	MediaType      string
	CanonicalBytes []byte
}

// ContentRecord is an immutable content-addressed row. CanonicalBytes is
// always detached from both caller-owned input and Store-owned storage.
type ContentRecord struct {
	Digest         string
	Kind           ContentKind
	MediaType      string
	CanonicalBytes []byte
	SizeBytes      int64
	CreatedAt      time.Time
}

// Valid reports whether kind is one of the frozen ContentRecord kinds.
func (kind ContentKind) Valid() bool {
	switch kind {
	case ContentModuleManifest,
		ContentConfig,
		ContentAuthorityCeiling,
		ContentTaskInput,
		ContentPolicy,
		ContentModelRequest,
		ContentContextCompilation,
		ContentModelResult,
		ContentActionProposal,
		ContentActionResult,
		ContentProviderReceipt,
		ContentStaticContext,
		ContentMemorySnapshot,
		ContentReconciliationEvidence,
		ContentRunEventPayload,
		ContentRunCancellation,
		ContentChannelCursor,
		ContentChannelIngressEnvelope,
		ContentChannelSendProposal,
		ContentChannelSendResult,
		ContentWorkspaceTransferPayload,
		ContentWorkspaceTransferEnvelope:
		return true
	default:
		return false
	}
}

// ComputeContentDigest validates the immutable content body and computes:
//
//	SHA256(
//	  "freeagent.content-record/v1\0" +
//	  kind + NUL + media_type + NUL + canonical_bytes
//	)
//
// JSON media types must already contain their exact RFC 8785 representation.
// Other media types are hashed byte-for-byte without text normalization.
func ComputeContentDigest(
	kind ContentKind,
	mediaType string,
	canonicalBytes []byte,
) (string, error) {
	if err := validateContentBody(kind, mediaType, canonicalBytes); err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(contentDigestDomain))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(mediaType))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonicalBytes)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// PutContent atomically inserts an immutable ContentRecord. Repeating the
// exact same row is idempotent. A pre-existing digest with any different
// immutable field is a fail-closed integrity error.
func (store *Store) PutContent(
	ctx context.Context,
	input ContentInput,
) (ContentRecord, error) {
	if ctx == nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidContent,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ContentRecord{}, err
	}
	defer unlock()

	body := bytes.Clone(input.CanonicalBytes)
	expectedDigest, err := ComputeContentDigest(
		input.Kind,
		input.MediaType,
		body,
	)
	if err != nil {
		return ContentRecord{}, err
	}
	if err := validateDigest(input.Digest); err != nil {
		return ContentRecord{}, err
	}
	if input.Digest != expectedDigest {
		return ContentRecord{}, fmt.Errorf(
			"%w: digest %s does not match computed digest %s",
			ErrContentIntegrity,
			input.Digest,
			expectedDigest,
		)
	}

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"currentstore: acquire content connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return ContentRecord{}, fmt.Errorf(
			"currentstore: begin PutContent: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	createdAtMicros := nowUnixMicro()
	createdAt, _ := timeFromUnixMicro(createdAtMicros)
	result, err := connection.ExecContext(ctx, `
		INSERT INTO content_records(
			content_digest,
			kind,
			media_type,
			canonical_bytes,
			size_bytes,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(content_digest) DO NOTHING
	`,
		input.Digest,
		string(input.Kind),
		input.MediaType,
		body,
		len(body),
		createdAtMicros,
	)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"currentstore: insert content %s: %w",
			input.Digest,
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"currentstore: inspect content insert: %w",
			err,
		)
	}

	var record ContentRecord
	if affected == 1 {
		record = ContentRecord{
			Digest:         input.Digest,
			Kind:           input.Kind,
			MediaType:      input.MediaType,
			CanonicalBytes: bytes.Clone(body),
			SizeBytes:      int64(len(body)),
			CreatedAt:      createdAt,
		}
	} else if affected == 0 {
		record, err = queryContent(ctx, connection, input.Digest)
		if err != nil {
			return ContentRecord{}, err
		}
		if record.Digest != input.Digest ||
			record.Kind != input.Kind ||
			record.MediaType != input.MediaType ||
			record.SizeBytes != int64(len(body)) ||
			!bytes.Equal(record.CanonicalBytes, body) {
			return ContentRecord{}, fmt.Errorf(
				"%w: stored row for digest %s differs",
				ErrContentIntegrity,
				input.Digest,
			)
		}
	} else {
		return ContentRecord{}, fmt.Errorf(
			"%w: content insert affected %d rows",
			ErrContentIntegrity,
			affected,
		)
	}

	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ContentRecord{}, fmt.Errorf(
			"currentstore: commit PutContent: %w",
			err,
		)
	}
	committed = true
	return cloneContentRecord(record), nil
}

// GetContent returns the immutable row addressed by digest. The returned byte
// slice is a defensive copy and may be modified by the caller.
func (store *Store) GetContent(
	ctx context.Context,
	digest string,
) (ContentRecord, error) {
	if ctx == nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidContent,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ContentRecord{}, err
	}
	defer unlock()
	if err := validateDigest(digest); err != nil {
		return ContentRecord{}, err
	}

	record, err := queryContent(ctx, store.db, digest)
	if err != nil {
		return ContentRecord{}, err
	}
	return cloneContentRecord(record), nil
}

func (store *Store) lockOpen() (func(), error) {
	if store == nil {
		return nil, ErrStoreClosed
	}
	store.closeMu.Lock()
	if store.closed || store.db == nil {
		store.closeMu.Unlock()
		return nil, ErrStoreClosed
	}
	return store.closeMu.Unlock, nil
}

func queryContent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	digest string,
) (ContentRecord, error) {
	var (
		record    ContentRecord
		kind      string
		createdAt int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			content_digest,
			kind,
			media_type,
			canonical_bytes,
			size_bytes,
			created_at
		FROM content_records
		WHERE content_digest=?
	`, digest).Scan(
		&record.Digest,
		&kind,
		&record.MediaType,
		&record.CanonicalBytes,
		&record.SizeBytes,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentRecord{}, fmt.Errorf(
			"%w: %s",
			ErrContentNotFound,
			digest,
		)
	}
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"currentstore: read content %s: %w",
			digest,
			err,
		)
	}
	record.Kind = ContentKind(kind)
	parsedTime, err := timeFromUnixMicro(createdAt)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: content %s has invalid created_at",
			ErrContentIntegrity,
			digest,
		)
	}
	record.CreatedAt = parsedTime
	if record.SizeBytes != int64(len(record.CanonicalBytes)) {
		return ContentRecord{}, fmt.Errorf(
			"%w: content %s has inconsistent size",
			ErrContentIntegrity,
			digest,
		)
	}
	computed, err := ComputeContentDigest(
		record.Kind,
		record.MediaType,
		record.CanonicalBytes,
	)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: content %s has invalid immutable fields: %v",
			ErrContentIntegrity,
			digest,
			err,
		)
	}
	if record.Digest != digest || computed != record.Digest {
		return ContentRecord{}, fmt.Errorf(
			"%w: content %s digest does not match stored bytes",
			ErrContentIntegrity,
			digest,
		)
	}
	return record, nil
}

func validateContentBody(
	kind ContentKind,
	mediaType string,
	canonicalBytes []byte,
) error {
	if !kind.Valid() {
		return fmt.Errorf(
			"%w: unsupported kind %q",
			ErrInvalidContent,
			kind,
		)
	}
	if mediaType == "" || mediaType != strings.TrimSpace(mediaType) ||
		len(mediaType) > 256 {
		return fmt.Errorf(
			"%w: media type must be non-empty, trimmed, and at most 256 bytes",
			ErrInvalidContent,
		)
	}
	baseType, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return fmt.Errorf(
			"%w: parse media type %q: %v",
			ErrInvalidContent,
			mediaType,
			err,
		)
	}
	baseType = strings.ToLower(baseType)
	if baseType == "application/json" ||
		strings.HasSuffix(baseType, "+json") {
		canonical, err := moduleapi.CanonicalJSON(canonicalBytes)
		if err != nil {
			return fmt.Errorf(
				"%w: invalid RFC 8785 JSON: %v",
				ErrInvalidContent,
				err,
			)
		}
		if !bytes.Equal(canonical, canonicalBytes) {
			return fmt.Errorf(
				"%w: JSON bytes are not canonical RFC 8785",
				ErrInvalidContent,
			)
		}
	}
	return nil
}

func validateDigest(digest string) error {
	if len(digest) != sha256.Size*2 {
		return fmt.Errorf(
			"%w: digest must be 64 lowercase hexadecimal characters",
			ErrInvalidContent,
		)
	}
	for _, value := range []byte(digest) {
		if (value < '0' || value > '9') &&
			(value < 'a' || value > 'f') {
			return fmt.Errorf(
				"%w: digest must be 64 lowercase hexadecimal characters",
				ErrInvalidContent,
			)
		}
	}
	return nil
}

func cloneContentRecord(record ContentRecord) ContentRecord {
	record.CanonicalBytes = bytes.Clone(record.CanonicalBytes)
	return record
}
