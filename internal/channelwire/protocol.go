// Package channelwire is the versioned transport contract. It has no model,
// execution, database or platform client dependencies.
package channelwire

import "time"

const Version = 3

// TextEntity uses UTF-16 offsets, matching the platform wire convention.
// Only the transport formatter creates entities; model text is never trusted
// as preformatted platform markup.
type TextEntity struct {
	Type     string `json:"type"`
	Offset   int    `json:"offset"`
	Length   int    `json:"length"`
	URL      string `json:"url,omitempty"`
	Language string `json:"language,omitempty"`
}

type TextPart struct {
	LogicalPart  int          `json:"logical_part,omitempty"`
	PlatformPart int          `json:"platform_part,omitempty"`
	Text         string       `json:"text"`
	Entities     []TextEntity `json:"entities,omitempty"`
}

// Activity is a short-lived hint, not a durable delivery or message.
type Activity struct {
	Target  Target `json:"target"`
	Action  string `json:"action"`
	UntilMS int64  `json:"until_ms"`
}

type Acceptance struct {
	Received    bool   `json:"received"`
	Disposition string `json:"disposition"`
}

type MediaRef struct {
	PreviewFormat string `json:"preview_format,omitempty"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Bytes         int64  `json:"bytes"`
	Image         bool   `json:"image,omitempty"`
}
type Target struct {
	Source        string `json:"source"`
	ApplicationID string `json:"application_id"`
	ChatID        string `json:"chat_id"`
}
type Event struct {
	Sticker         *Sticker   `json:"sticker,omitempty"`
	Reaction        *Reaction  `json:"reaction,omitempty"`
	ID              string     `json:"id"`
	Target          Target     `json:"target"`
	Person          string     `json:"person"`
	Name            string     `json:"name"`
	Message         string     `json:"message"`
	Kind            string     `json:"kind"`
	Text            string     `json:"text"`
	Trigger         bool       `json:"trigger"`
	Callback        string     `json:"callback,omitempty"`
	CallbackMessage string     `json:"callback_message,omitempty"`
	Media           []MediaRef `json:"media,omitempty"`
	ReplyTo         string     `json:"reply_to,omitempty"`
}
type Button struct {
	Text  string `json:"text"`
	Token string `json:"token"`
}
type Operation struct {
	WakeID              string       `json:"wake_id,omitempty"`
	SourceHash          string       `json:"source_hash,omitempty"`
	CandidateDigest     string       `json:"candidate_digest,omitempty"`
	Proactive           bool         `json:"proactive,omitempty"`
	CandidateID         string       `json:"candidate_id,omitempty"`
	PayloadDigest       string       `json:"payload_digest,omitempty"`
	Extension           string       `json:"extension,omitempty"`
	RuleID              string       `json:"rule_id,omitempty"`
	RuleDigest          string       `json:"rule_digest,omitempty"`
	Timezone            string       `json:"timezone,omitempty"`
	InputBound          int64        `json:"input_bound,omitempty"`
	ReviewUntilMS       int64        `json:"review_until_ms,omitempty"`
	SuppressedReason    string       `json:"suppressed_reason,omitempty"`
	MinGapMS            int          `json:"min_gap_ms,omitempty"`
	ConfirmedMS         int64        `json:"confirmed_ms,omitempty"`
	LogicalPart         int          `json:"logical_part,omitempty"`
	PlatformPart        int          `json:"platform_part,omitempty"`
	NotBeforeMS         int64        `json:"not_before_ms,omitempty"`
	ExpiresMS           int64        `json:"expires_ms,omitempty"`
	ReviewedUncertainty bool         `json:"reviewed_uncertainty,omitempty"`
	ResumedMS           int64        `json:"resumed_ms,omitempty"`
	PageOffset          int          `json:"page_offset,omitempty"`
	PageSize            int          `json:"page_size,omitempty"`
	Sticker             *Sticker     `json:"sticker,omitempty"`
	Reaction            string       `json:"reaction,omitempty"`
	Group               string       `json:"group,omitempty"`
	Sequence            int          `json:"sequence,omitempty"`
	Total               int          `json:"total,omitempty"`
	Entities            []TextEntity `json:"entities,omitempty"`
	FormatVersion       string       `json:"format_version,omitempty"`
	PlainTextFallback   bool         `json:"plain_text_fallback,omitempty"`
	LocalOwner          bool         `json:"local_owner,omitempty"`
	Projected           bool         `json:"projected,omitempty"`
	Dispatches          []Dispatch   `json:"dispatches,omitempty"`
	NextDispatchMS      int64        `json:"next_dispatch_ms,omitempty"`
	DataSHA256          string       `json:"data_sha256,omitempty"`
	DataBytes           int64        `json:"data_bytes,omitempty"`
	ClaimKey            string       `json:"claim_key,omitempty"`
	Maintenance         bool         `json:"maintenance,omitempty"`
	Person              string       `json:"person,omitempty"`
	AccessRevision      uint64       `json:"access_revision,omitempty"`
	Admission           bool         `json:"admission,omitempty"`
	Dispatch            string       `json:"dispatch,omitempty"`
	Attempts            int          `json:"attempts"`
	LastDispatch        string       `json:"last_dispatch,omitempty"`
	AttachmentID        string       `json:"attachment_id,omitempty"`
	ConversationID      string       `json:"conversation_id,omitempty"`
	DataFile            string       `json:"-"`
	ID                  string       `json:"id"`
	Connection          string       `json:"connection"`
	Kind                string       `json:"kind"`
	Target              Target       `json:"target"`
	Reply               string       `json:"reply,omitempty"`
	Text                string       `json:"text,omitempty"`
	Buttons             []Button     `json:"buttons,omitempty"`
	Media               *MediaRef    `json:"media,omitempty"`
	Name                string       `json:"name,omitempty"`
	Data                []byte       `json:"data,omitempty"`
	Limit               int64        `json:"limit,omitempty"`
	RunID               string       `json:"run_id,omitempty"`
	State               string       `json:"state"`
	MessageID           string       `json:"message_id,omitempty"`
	CreatedAt           string       `json:"created_at"`
}

// Dispatch records preserve each transport attempt, including late receipts.
// They do not authorize a second execution of the associated Core Run.
type Dispatch struct {
	ID           string `json:"id"`
	At           string `json:"at"`
	State        string `json:"state"`
	MessageID    string `json:"message_id,omitempty"`
	RetryAfterMS int64  `json:"retry_after_ms,omitempty"`
}
type Receipt struct {
	PlainTextFallback bool   `json:"plain_text_fallback,omitempty"`
	Dispatch          string `json:"dispatch"`
	ID                string `json:"id"`
	State             string `json:"state"`
	MessageID         string `json:"message_id,omitempty"`
	Data              []byte `json:"data,omitempty"`
	RetryAfterMS      int64  `json:"retry_after_ms,omitempty"`
}
type Connection struct {
	AutonomousReactions bool   `json:"autonomous_reactions,omitempty"`
	ReplyStickers       bool   `json:"reply_stickers,omitempty"`
	PlainText           bool   `json:"plain_text,omitempty"`
	DisableTyping       bool   `json:"disable_typing,omitempty"`
	ID                  string `json:"id"`
	Source              string `json:"source"`
	ApplicationID       string `json:"application_id"`
	Credential          string `json:"credential"`
	Enabled             bool   `json:"enabled"`
	Listen              string `json:"listen,omitempty"`
	DropPendingBefore   string `json:"drop_pending_before,omitempty"`
}
type Bootstrap struct {
	Version    int        `json:"version"`
	CoreID     string     `json:"core_id"`
	Generation string     `json:"generation"`
	Origin     string     `json:"origin"`
	Connection Connection `json:"connection"`
	Token      string     `json:"token"`
}
type Credential struct {
	BaseURL string `json:"base_url"`
	Secret  string `json:"secret"`
}
type Error struct {
	Certain bool          `json:"certain"`
	Wait    time.Duration `json:"wait"`
}

func (e *Error) Error() string             { return "channel: transport operation did not complete" }
func (e *Error) DispatchCertain() bool     { return e.Certain }
func (e *Error) RetryDelay() time.Duration { return e.Wait }

// SplitText is a conservative plain-text partition in UTF-16 units. Platform
// formatters can use SplitTextParts to keep style ranges when preparing output.
func SplitText(text string) []string {
	var result []string
	for _, part := range SplitTextParts(TextPart{Text: text}, 3500) {
		result = append(result, part.Text)
	}
	return result
}
