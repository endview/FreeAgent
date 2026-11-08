package channelwire

// Sticker is authenticated transport metadata. Models receive only a scoped
// Core reference; file_id never authorizes an arbitrary destination.
type Sticker struct {
	Preview   *MediaRef `json:"preview,omitempty"`
	FileID    string    `json:"file_id"`
	UniqueID  string    `json:"file_unique_id"`
	SetName   string    `json:"set_name,omitempty"`
	Emoji     string    `json:"emoji,omitempty"`
	Format    string    `json:"format"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Bytes     int64     `json:"bytes,omitempty"`
	Thumbnail *MediaRef `json:"thumbnail,omitempty"`
}

type StickerPage struct {
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	Total    int       `json:"total"`
	Offset   int       `json:"offset"`
	Stickers []Sticker `json:"stickers"`
}

type Reaction struct {
	Message   string   `json:"message"`
	Old       []string `json:"old"`
	New       []string `json:"new"`
	Date      int64    `json:"date"`
	Anonymous bool     `json:"anonymous,omitempty"`
	// Counts is an absolute anonymous snapshot, never per-person feedback.
	// CountSnapshot distinguishes complete revocation from an actor event,
	// including when an empty Counts slice is omitted during serialization.
	Counts        []ReactionCount `json:"counts,omitempty"`
	CountSnapshot bool            `json:"count_snapshot,omitempty"`
}

type ReactionCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}
