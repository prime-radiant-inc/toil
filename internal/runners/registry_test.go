package runners

import (
	"context"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if reg.runners == nil {
		t.Fatal("runners map should be initialized")
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	dummy := &ShellRunner{}

	if err := reg.Register("shell", dummy); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := reg.Get("shell")
	if !ok {
		t.Fatal("expected to find registered runner")
	}
	if got != dummy {
		t.Fatal("Get returned different runner than registered")
	}
}

func TestRegistry_RegisterEmptyID(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register("", &ShellRunner{})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("shell", &ShellRunner{})

	err := reg.Register("shell", &ShellRunner{})
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestRegistry_GetMiss(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Fatal("expected ok=false for missing runner")
	}
}

func TestRegistry_MultipleRunners(t *testing.T) {
	reg := NewRegistry()

	shell := &ShellRunner{}
	human := &HumanRunner{}

	_ = reg.Register("shell", shell)
	_ = reg.Register("human", human)

	gotShell, ok := reg.Get("shell")
	if !ok || gotShell != shell {
		t.Fatal("shell runner not found or wrong instance")
	}

	gotHuman, ok := reg.Get("human")
	if !ok || gotHuman != human {
		t.Fatal("human runner not found or wrong instance")
	}
}

// stubRunner is a minimal Runner for testing registry storage of the interface.
type stubRunner struct {
	id string
}

func (s *stubRunner) Run(ctx context.Context, req Request, handler LineHandler) (Result, error) {
	return Result{Output: s.id}, nil
}

func TestRegistry_StoresRunnerInterface(t *testing.T) {
	reg := NewRegistry()
	stub := &stubRunner{id: "test-runner"}

	if err := reg.Register("stub", stub); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := reg.Get("stub")
	if !ok {
		t.Fatal("expected to find stub runner")
	}

	result, err := got.Run(context.Background(), Request{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Output != "test-runner" {
		t.Fatalf("Output = %q, want %q", result.Output, "test-runner")
	}
}
