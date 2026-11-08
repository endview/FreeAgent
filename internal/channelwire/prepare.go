package channelwire

// Capabilities describes only the bounded actions implemented by this channel
// connection. It cannot grant a recipient, file, model or command capability.
type Capabilities struct {
	Version   int      `json:"version"`
	Format    string   `json:"format"`
	Actions   []string `json:"actions"`
	Reactions []string `json:"reactions,omitempty"`
}

type Prepared struct {
	SourceHash string     `json:"source_hash"`
	Format     string     `json:"format,omitempty"`
	Parts      []TextPart `json:"parts,omitempty"`
	Resource   string     `json:"resource,omitempty"`
}

func PurePreparation(kind string) bool { return kind == "prepare_text" || kind == "prepare_resource" }
