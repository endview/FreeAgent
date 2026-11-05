// Package channelwire is the versioned transport contract. It has no model,
// execution, database or platform client dependencies.
package channelwire

import "time"

const Version = 1

type Acceptance struct {
	Received    bool   `json:"received"`
	Disposition string `json:"disposition"`
}

type MediaRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Image bool   `json:"image,omitempty"`
}
type Target struct {
	Source        string `json:"source"`
	ApplicationID string `json:"application_id"`
	ChatID        string `json:"chat_id"`
}
type Event struct {
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
	LocalOwner     bool       `json:"local_owner,omitempty"`
	Projected      bool       `json:"projected,omitempty"`
	Dispatches     []Dispatch `json:"dispatches,omitempty"`
	NextDispatchMS int64      `json:"next_dispatch_ms,omitempty"`
	DataSHA256     string     `json:"data_sha256,omitempty"`
	DataBytes      int64      `json:"data_bytes,omitempty"`
	ClaimKey       string     `json:"claim_key,omitempty"`
	Maintenance    bool       `json:"maintenance,omitempty"`
	Person         string     `json:"person,omitempty"`
	AccessRevision uint64     `json:"access_revision,omitempty"`
	Admission      bool       `json:"admission,omitempty"`
	Dispatch       string     `json:"dispatch,omitempty"`
	Attempts       int        `json:"attempts"`
	LastDispatch   string     `json:"last_dispatch,omitempty"`
	AttachmentID   string     `json:"attachment_id,omitempty"`
	ConversationID string     `json:"conversation_id,omitempty"`
	DataFile       string     `json:"-"`
	ID             string     `json:"id"`
	Connection     string     `json:"connection"`
	Kind           string     `json:"kind"`
	Target         Target     `json:"target"`
	Reply          string     `json:"reply,omitempty"`
	Text           string     `json:"text,omitempty"`
	Buttons        []Button   `json:"buttons,omitempty"`
	Media          *MediaRef  `json:"media,omitempty"`
	Name           string     `json:"name,omitempty"`
	Data           []byte     `json:"data,omitempty"`
	Limit          int64      `json:"limit,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	State          string     `json:"state"`
	MessageID      string     `json:"message_id,omitempty"`
	CreatedAt      string     `json:"created_at"`
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
	Dispatch     string `json:"dispatch"`
	ID           string `json:"id"`
	State        string `json:"state"`
	MessageID    string `json:"message_id,omitempty"`
	Data         []byte `json:"data,omitempty"`
	RetryAfterMS int64  `json:"retry_after_ms,omitempty"`
}
type Connection struct {
	ID                string `json:"id"`
	Source            string `json:"source"`
	ApplicationID     string `json:"application_id"`
	Credential        string `json:"credential"`
	Enabled           bool   `json:"enabled"`
	Listen            string `json:"listen,omitempty"`
	DropPendingBefore string `json:"drop_pending_before,omitempty"`
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

// SplitText is only a bounded plain-text partition. Platform rendering belongs
// to the channel process, which may split these parts further.
func SplitText(text string) []string {
	runes := []rune(text)
	var result []string
	for len(runes) > 0 {
		n := min(len(runes), 3500)
		result = append(result, string(runes[:n]))
		runes = runes[n:]
	}
	return result
}
