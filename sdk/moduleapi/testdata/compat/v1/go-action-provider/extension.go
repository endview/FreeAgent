// Package extension is a synthetic, effect-free compatibility fixture. It is
// compiled from a separate temporary Go module by external_import_test.go.
package extension

import (
	"context"
	"encoding/json"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type Provider struct{}

func (Provider) Describe(
	context.Context,
	moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	return nil, nil
}

func (Provider) Prepare(
	context.Context,
	moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

var _ moduleapi.ActionProviderV1 = Provider{}
