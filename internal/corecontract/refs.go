package corecontract

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	maxOpaqueIDBytes = 256
	maxVersionBytes  = 64
)

// AgentRef is an immutable Agent definition reference.
type AgentRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref AgentRef) Validate() error {
	return validateTypedRef("agent", ref.ID, ref.Version, ref.Digest)
}

// ProfileRef is an immutable assembly Profile reference.
type ProfileRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref ProfileRef) Validate() error {
	return validateTypedRef("profile", ref.ID, ref.Version, ref.Digest)
}

// ModelProfileRef is an immutable reference to one canonical model-profile/v1
// CONFIG content record. It is independent of the assembly ProfileRef: the
// former describes one exact model build, while the latter selects an Agent
// assembly.
type ModelProfileRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref ModelProfileRef) Validate() error {
	return validateTypedRef(
		"model profile",
		ref.ID,
		ref.Version,
		ref.Digest,
	)
}

// WorkspaceRef is an immutable Workspace definition reference.
type WorkspaceRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref WorkspaceRef) Validate() error {
	return validateTypedRef("workspace", ref.ID, ref.Version, ref.Digest)
}

// TaskRef is an immutable task definition reference.
type TaskRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref TaskRef) Validate() error {
	return validateTypedRef("task", ref.ID, ref.Version, ref.Digest)
}

// PolicyRef is an immutable POLICY ContentRecord reference.
type PolicyRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref PolicyRef) Validate() error {
	return validateTypedRef("policy", ref.ID, ref.Version, ref.Digest)
}

// CatalogSnapshotRef is an immutable RuntimeCatalog generation reference.
type CatalogSnapshotRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref CatalogSnapshotRef) Validate() error {
	return validateTypedRef("catalog snapshot", ref.ID, ref.Version, ref.Digest)
}

// MemberSnapshotRef points to one immutable member snapshot within a Run.
type MemberSnapshotRef struct {
	MemberID string `json:"member_id"`
	Digest   string `json:"digest"`
}

func (ref MemberSnapshotRef) Validate() error {
	if !validOpaque(ref.MemberID, maxOpaqueIDBytes) {
		return fmt.Errorf("corecontract: invalid member snapshot member ID")
	}
	if !moduleapi.ValidSHA256(ref.Digest) {
		return fmt.Errorf("corecontract: invalid member snapshot digest")
	}
	return nil
}

func validateTypedRef(kind, id, version, digest string) error {
	if !validOpaque(id, maxOpaqueIDBytes) {
		return fmt.Errorf("corecontract: invalid %s ID", kind)
	}
	if !validOpaque(version, maxVersionBytes) {
		return fmt.Errorf("corecontract: invalid %s version", kind)
	}
	if !moduleapi.ValidSHA256(digest) {
		return fmt.Errorf("corecontract: invalid %s digest", kind)
	}
	return nil
}

func validOpaque(value string, maximum int) bool {
	if value == "" ||
		len(value) > maximum ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) ||
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
