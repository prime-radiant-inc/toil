package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailEvents_ReadsNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Write initial event
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"node_started","node_id":"a"}` + "\n")
	_ = f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch := TailEvents(ctx, path, 0)

	// Read initial event
	select {
	case e := <-ch:
		if e.Type != "node_started" {
			t.Errorf("type = %q, want node_started", e.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for initial event")
	}

	// Append new event
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(`{"type":"node_completed","node_id":"a"}` + "\n")
	_ = f.Close()

	// Read new event (may take up to 500ms poll interval)
	select {
	case e := <-ch:
		if e.Type != "node_completed" {
			t.Errorf("type = %q, want node_completed", e.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for appended event")
	}
}

func TestTailEvents_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	f, _ := os.Create(path)
	_, _ = f.WriteString(`{"type":"node_started","node_id":"a"}` + "\n")
	_ = f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch := TailEvents(ctx, path, 0)

	// Read initial
	<-ch

	// Cancel
	cancel()

	// Channel should close
	select {
	case _, ok := <-ch:
		_ = ok // may get one more event, but eventually should close
	case <-time.After(2 * time.Second):
		t.Fatal("channel didn't close after context cancellation")
	}
}

func TestTailEvents_StartOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Write two events
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"node_started","node_id":"a"}` + "\n")
	_, _ = f.WriteString(`{"type":"node_completed","node_id":"a"}` + "\n")
	_ = f.Close()

	// Get file size to use as start offset
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Tail from end of file -- should skip existing events
	ch := TailEvents(ctx, path, info.Size())

	// Append a new event
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(`{"type":"run_completed"}` + "\n")
	_ = f.Close()

	// Should only see the new event
	select {
	case e := <-ch:
		if e.Type != "run_completed" {
			t.Errorf("type = %q, want run_completed", e.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for event after offset")
	}
}

func TestTailEvents_FileNotYetCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch := TailEvents(ctx, path, 0)

	// Create file after a delay
	go func() {
		time.Sleep(600 * time.Millisecond)
		f, _ := os.Create(path)
		_, _ = f.WriteString(`{"type":"run_started"}` + "\n")
		_ = f.Close()
	}()

	select {
	case e := <-ch:
		if e.Type != "run_started" {
			t.Errorf("type = %q, want run_started", e.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for event after file creation")
	}
}

func TestTailEvents_PartialWriteRecoveredOnCompletion(t *testing.T) {
	// Regression guard: when a writer appends a line in two chunks
	// (first half without newline, then the rest with newline), the
	// tail must NOT skip the event. Before the fix, ReadString
	// consumed the partial bytes and the tail advanced offset past
	// them, so the completed event was never re-read.
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("create file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch := TailEvents(ctx, path, 0)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Write the first half of a JSONL line — no newline yet.
	firstHalf := `{"type":"run_started","run_id":"r1","data":{"filler":"`
	if _, err := f.WriteString(firstHalf); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	// Wait long enough that the tail polls once and sees only the partial.
	time.Sleep(700 * time.Millisecond)

	// Now complete the line.
	rest := `xxx"}}` + "\n"
	if _, err := f.WriteString(rest); err != nil {
		t.Fatalf("write rest: %v", err)
	}

	select {
	case e := <-ch:
		if e.Type != "run_started" {
			t.Fatalf("expected run_started, got %q", e.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout — partial line was not re-read after completion")
	}
}
