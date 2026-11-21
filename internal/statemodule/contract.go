// Package statemodule contains state protocol values shared by optional
// modules. It has no storage, identity policy, executor or transport.
package statemodule

import "encoding/json"

type Scope struct {
	CoreID   string `json:"core_id"`
	SpaceID  string `json:"space_id"`
	Resource string `json:"resource,omitempty"`
	ActorID  string `json:"actor_id"`
}

type Preview struct {
	Revision int64           `json:"revision"`
	Before   json.RawMessage `json:"before"`
	After    json.RawMessage `json:"after"`
	Summary  string          `json:"summary"`
}

type Result struct {
	Found       bool   `json:"found"`
	OperationID string `json:"operation_id"`
	Digest      string `json:"digest"`
	Revision    int64  `json:"revision"`
}
