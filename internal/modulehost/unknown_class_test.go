package modulehost

import "testing"

func TestValidateInvocationUnknownClassUsesClosedOutcomeMatrix(t *testing.T) {
	classes := []InvocationUnknownClass{
		UnknownClassInvokeReturnedError,
		UnknownClassNoUsableResponse,
		UnknownClassResponseBodyReadIncomplete,
	}
	for _, class := range classes {
		if err := ValidateInvocationUnknownClass(
			InvocationUnknown,
			class,
		); err != nil {
			t.Fatalf("UNKNOWN class %q rejected: %v", class, err)
		}
		for _, certain := range []InvocationOutcome{
			InvocationSucceeded,
			InvocationFailed,
		} {
			if err := ValidateInvocationUnknownClass(certain, class); err == nil {
				t.Fatalf("%s accepted UNKNOWN class %q", certain, class)
			}
		}
	}
	for _, outcome := range []InvocationOutcome{
		InvocationSucceeded,
		InvocationFailed,
		InvocationUnknown,
	} {
		if err := ValidateInvocationUnknownClass(outcome, ""); err != nil {
			t.Fatalf("legacy empty class for %s rejected: %v", outcome, err)
		}
	}
	const private = InvocationUnknownClass("private raw transport error")
	if err := ValidateInvocationUnknownClass(
		InvocationUnknown,
		private,
	); err == nil {
		t.Fatal("free-form UNKNOWN class was accepted")
	}
}
