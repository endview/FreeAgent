package modulehost

import "fmt"

// InvocationUnknownClass is a bounded, non-sensitive observation made at the
// provider boundary. It describes only where the local adapter lost certainty;
// it never proves that the provider received, executed, or completed a request.
type InvocationUnknownClass string

const (
	UnknownClassInvokeReturnedError        InvocationUnknownClass = "INVOKE_RETURNED_ERROR"
	UnknownClassNoUsableResponse           InvocationUnknownClass = "NO_USABLE_RESPONSE"
	UnknownClassResponseBodyReadIncomplete InvocationUnknownClass = "RESPONSE_BODY_READ_INCOMPLETE"
)

// ValidateInvocationUnknownClass enforces the closed observation-code set and
// prevents diagnostic data from appearing on a certain outcome.
func ValidateInvocationUnknownClass(
	outcome InvocationOutcome,
	class InvocationUnknownClass,
) error {
	if class == "" {
		return nil
	}
	if outcome != InvocationUnknown {
		return fmt.Errorf(
			"modulehost: unknown class is only valid for UNKNOWN outcomes",
		)
	}
	switch class {
	case UnknownClassInvokeReturnedError,
		UnknownClassNoUsableResponse,
		UnknownClassResponseBodyReadIncomplete:
		return nil
	default:
		return fmt.Errorf("modulehost: unsupported invocation unknown class")
	}
}
