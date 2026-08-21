package controlapp

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/moduledisablecontract"
)

var (
	_ moduledisablecontract.ModuleBindingTargetV1             = ModuleBindingTargetV1{}
	_ ModuleBindingTargetV1                                   = moduledisablecontract.ModuleBindingTargetV1{}
	_ moduledisablecontract.ModuleDisableDryRunBodyV1         = ModuleDisableDryRunBodyV1{}
	_ ModuleDisableDryRunBodyV1                               = moduledisablecontract.ModuleDisableDryRunBodyV1{}
	_ moduledisablecontract.ModuleDisableEvaluationV1         = ModuleDisableEvaluationV1{}
	_ ModuleDisableEvaluationV1                               = moduledisablecontract.ModuleDisableEvaluationV1{}
	_ moduledisablecontract.ModuleDisablePublicationReceiptV1 = ModuleDisablePublicationReceiptV1{}
	_ ModuleDisablePublicationReceiptV1                       = moduledisablecontract.ModuleDisablePublicationReceiptV1{}
)

func TestModuleDisablePureContractFacadeCompatibilityV1(t *testing.T) {
	body := testModuleDisableBodyV1()
	wrapperBody, wrapperBodyCanonical, wrapperBodyDigest, wrapperErr :=
		NewModuleDisableDryRunBodyV1(body)
	directBody, directBodyCanonical, directBodyDigest, directErr :=
		moduledisablecontract.NewModuleDisableDryRunBodyV1(body)
	if wrapperErr != nil || directErr != nil || wrapperBody != directBody ||
		wrapperBodyDigest != directBodyDigest ||
		!bytes.Equal(wrapperBodyCanonical, directBodyCanonical) {
		t.Fatalf("body facade drift: wrapper=%+v/%s/%v direct=%+v/%s/%v",
			wrapperBody, wrapperBodyDigest, wrapperErr,
			directBody, directBodyDigest, directErr,
		)
	}

	evaluation := validModuleDisableEvaluationFixtureV1(t)
	wrapperEvaluation, wrapperEvaluationCanonical, wrapperEvaluationDigest, wrapperErr :=
		NewModuleDisableEvaluationV1(evaluation)
	directEvaluation, directEvaluationCanonical, directEvaluationDigest, directErr :=
		moduledisablecontract.NewModuleDisableEvaluationV1(evaluation)
	if wrapperErr != nil || directErr != nil ||
		!reflect.DeepEqual(wrapperEvaluation, directEvaluation) ||
		wrapperEvaluationDigest != directEvaluationDigest ||
		!bytes.Equal(wrapperEvaluationCanonical, directEvaluationCanonical) {
		t.Fatalf("evaluation facade drift: wrapper=%s/%v direct=%s/%v",
			wrapperEvaluationDigest, wrapperErr,
			directEvaluationDigest, directErr,
		)
	}

	publication := validModuleDisablePublicationReceiptFixtureV1(t)
	wrapperPublication, wrapperPublicationCanonical, wrapperPublicationRef, wrapperErr :=
		NewModuleDisablePublicationReceiptV1(publication)
	directPublication, directPublicationCanonical, directPublicationRef, directErr :=
		moduledisablecontract.NewModuleDisablePublicationReceiptV1(publication)
	if wrapperErr != nil || directErr != nil ||
		!reflect.DeepEqual(wrapperPublication, directPublication) ||
		wrapperPublicationRef != directPublicationRef ||
		!bytes.Equal(wrapperPublicationCanonical, directPublicationCanonical) {
		t.Fatalf("publication facade drift: wrapper=%+v/%v direct=%+v/%v",
			wrapperPublicationRef, wrapperErr,
			directPublicationRef, directErr,
		)
	}
}

func TestModuleDisablePureContractFacadeMapsInvalidErrorsV1(t *testing.T) {
	if _, _, _, err := NewModuleDisableDryRunBodyV1(ModuleDisableDryRunBodyV1{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("body error=%v", err)
	}
	if _, err := RestoreModuleDisableEvaluationV1([]byte(`{}`), "bad"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("evaluation error=%v", err)
	}
	if _, _, _, err := NewModuleDisablePublicationReceiptV1(
		ModuleDisablePublicationReceiptV1{},
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("publication error=%v", err)
	}
}
