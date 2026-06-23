package main

import (
	"flag"
	"reflect"
	"testing"
)

// parseRunArgs is the helper under test. It exercises the same flag
// definitions runWorkflow uses, but returns the parsed values instead of
// exiting so tests can assert on them.
func parseRunArgs(t *testing.T, args []string) (workflowID string, inputs []string, err error) {
	t.Helper()
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	list := &inputList{}
	flags.Var(list, "input", "workflow input key=value (repeatable)")
	reordered := reorderRunArgs(args, map[string]bool{"input": true})
	if err = flags.Parse(reordered); err != nil {
		return "", nil, err
	}
	if flags.NArg() < 1 {
		return "", []string(*list), errMissingWorkflowID
	}
	return flags.Arg(0), []string(*list), nil
}

func TestReorderRunArgs_FlagFirst(t *testing.T) {
	wf, inputs, err := parseRunArgs(t, []string{"--input", "k=v", "wf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf != "wf" {
		t.Errorf("workflow = %q, want %q", wf, "wf")
	}
	if !reflect.DeepEqual(inputs, []string{"k=v"}) {
		t.Errorf("inputs = %v, want [k=v]", inputs)
	}
}

func TestReorderRunArgs_PositionalFirst(t *testing.T) {
	// THE BUG: this previously dropped --input silently because stdlib
	// flag.Parse stops at the first positional.
	wf, inputs, err := parseRunArgs(t, []string{"wf", "--input", "k=v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf != "wf" {
		t.Errorf("workflow = %q, want %q", wf, "wf")
	}
	if !reflect.DeepEqual(inputs, []string{"k=v"}) {
		t.Errorf("inputs = %v, want [k=v]", inputs)
	}
}

func TestReorderRunArgs_Interleaved(t *testing.T) {
	wf, inputs, err := parseRunArgs(t, []string{"--input", "a=1", "wf", "--input", "b=2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf != "wf" {
		t.Errorf("workflow = %q, want %q", wf, "wf")
	}
	if !reflect.DeepEqual(inputs, []string{"a=1", "b=2"}) {
		t.Errorf("inputs = %v, want [a=1 b=2]", inputs)
	}
}

func TestReorderRunArgs_MultipleInputsFlagFirst(t *testing.T) {
	wf, inputs, err := parseRunArgs(t, []string{"--input", "a=1", "--input", "b=2", "wf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf != "wf" {
		t.Errorf("workflow = %q, want %q", wf, "wf")
	}
	if !reflect.DeepEqual(inputs, []string{"a=1", "b=2"}) {
		t.Errorf("inputs = %v, want [a=1 b=2]", inputs)
	}
}

func TestReorderRunArgs_MultipleInputsPositionalFirst(t *testing.T) {
	wf, inputs, err := parseRunArgs(t, []string{"wf", "--input", "a=1", "--input", "b=2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf != "wf" {
		t.Errorf("workflow = %q, want %q", wf, "wf")
	}
	if !reflect.DeepEqual(inputs, []string{"a=1", "b=2"}) {
		t.Errorf("inputs = %v, want [a=1 b=2]", inputs)
	}
}

func TestReorderRunArgs_EqualsForm(t *testing.T) {
	// --input=key=value (single token) must also work, in either position.
	wf, inputs, err := parseRunArgs(t, []string{"wf", "--input=k=v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf != "wf" {
		t.Errorf("workflow = %q, want %q", wf, "wf")
	}
	if !reflect.DeepEqual(inputs, []string{"k=v"}) {
		t.Errorf("inputs = %v, want [k=v]", inputs)
	}
}

func TestReorderRunArgs_MissingWorkflowID(t *testing.T) {
	_, _, err := parseRunArgs(t, []string{"--input", "k=v"})
	if err != errMissingWorkflowID {
		t.Fatalf("err = %v, want errMissingWorkflowID", err)
	}
}
