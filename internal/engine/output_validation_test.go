package engine

import (
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestValidateNodeOutput_NilDataAllowed(t *testing.T) {
	node := &definitions.Node{
		Decisions: definitions.StringDecisions("push", "skip"),
	}

	out := NodeOutput{
		Decision: "push",
		Message:  "Pushing to remote",
		Data:     nil,
	}

	err := validateNodeOutput(out, node)
	if err != nil {
		t.Fatalf("expected no error for nil data, got: %v", err)
	}
}

func TestValidateNodeOutput_MissingDecisionIsError(t *testing.T) {
	node := &definitions.Node{
		Decisions: definitions.StringDecisions("done"),
	}

	out := NodeOutput{
		Message: "missing decision",
	}

	err := validateNodeOutput(out, node)
	if err == nil {
		t.Fatal("expected error for missing decision")
	}
	if !strings.Contains(err.Error(), `"decision"`) {
		t.Fatalf("expected error to mention decision, got: %v", err)
	}
}

func TestValidateNodeOutput_MissingMessageIsError(t *testing.T) {
	node := &definitions.Node{
		Decisions: definitions.StringDecisions("done"),
	}

	out := NodeOutput{
		Decision: "done",
	}

	err := validateNodeOutput(out, node)
	if err == nil {
		t.Fatal("expected error for missing message")
	}
	if !strings.Contains(err.Error(), `"message"`) {
		t.Fatalf("expected error to mention message, got: %v", err)
	}
}

func TestValidateNodeOutput_UnknownDecisionIsError(t *testing.T) {
	node := &definitions.Node{
		Decisions: definitions.StringDecisions("pass", "fail"),
	}

	out := NodeOutput{
		Decision: "maybe",
		Message:  "non-enum decision",
	}

	err := validateNodeOutput(out, node)
	if err == nil {
		t.Fatal("expected error for non-enum decision")
	}
	if !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("expected error about decision enum, got: %v", err)
	}
}
