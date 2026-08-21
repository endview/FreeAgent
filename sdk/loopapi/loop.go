package loopapi

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxOpaqueIDBytes = 256

// Disposition describes why one bounded Loop.Run invocation returned control
// to its caller. Waiting is a successful, persisted outcome; it is never an
// instruction to replay an external effect.
type Disposition string

const (
	DispositionYielded               Disposition = "YIELDED"
	DispositionWaitingInput          Disposition = "WAITING_INPUT"
	DispositionWaitingExternal       Disposition = "WAITING_EXTERNAL"
	DispositionWaitingReconciliation Disposition = "WAITING_RECONCILIATION"
	DispositionTerminated            Disposition = "TERMINATED"
)

func (disposition Disposition) Validate() error {
	switch disposition {
	case DispositionYielded,
		DispositionWaitingInput,
		DispositionWaitingExternal,
		DispositionWaitingReconciliation,
		DispositionTerminated:
		return nil
	default:
		return fmt.Errorf(
			"loopapi: unsupported disposition %q",
			disposition,
		)
	}
}

// RunInput is the complete public input to one bounded Loop.Run invocation.
// The limits may only narrow the budget frozen in the RunManifest. Provider
// bindings, policy, context, history, payloads, leases, and grants remain
// behind separately validated Core ports.
type RunInput struct {
	RunID       string
	MaxSteps    uint32
	MaxDuration time.Duration
}

func (input RunInput) Validate() error {
	if !validOpaqueID(input.RunID) {
		return fmt.Errorf("loopapi: invalid run ID")
	}
	if input.MaxSteps == 0 {
		return fmt.Errorf("loopapi: max steps must be positive")
	}
	if input.MaxDuration <= 0 {
		return fmt.Errorf("loopapi: max duration must be positive")
	}
	return nil
}

// RunResult exposes the persisted frame revision reached by this bounded
// advance. Internal frame documents and persistence schemas are deliberately
// not public Loop API.
type RunResult struct {
	RunID         string
	Disposition   Disposition
	FrameRevision uint64
	ReasonCode    string
}

func (result RunResult) Validate() error {
	if !validOpaqueID(result.RunID) {
		return fmt.Errorf("loopapi: invalid run ID")
	}
	if err := result.Disposition.Validate(); err != nil {
		return err
	}
	if !validOpaqueID(result.ReasonCode) {
		return fmt.Errorf("loopapi: invalid reason code")
	}
	return nil
}

// Loop is the single public Universal Loop contract. Implementations receive
// no authority from satisfying this interface.
type Loop interface {
	Run(context.Context, RunInput) (RunResult, error)
}

func validOpaqueID(value string) bool {
	if value == "" ||
		len(value) > MaxOpaqueIDBytes ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
