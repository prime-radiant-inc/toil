package interrogate

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

type fakeRunner struct {
	lastRequest runners.Request
	result      runners.Result
}

func (r *fakeRunner) Run(_ context.Context, req runners.Request, handler runners.LineHandler) (runners.Result, error) {
	r.lastRequest = req
	if handler != nil {
		handler(runners.Line{Stream: "stdout", Text: r.result.Output})
	}
	return r.result, nil
}

func TestManager_Create(t *testing.T) {
	fr := &fakeRunner{result: runners.Result{
		Output:    "forked response",
		SessionID: "forked-sess-123",
	}}

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.SessionID = "orig-sess-456"
	})

	mgr := NewManager()
	res, err := mgr.Create(context.Background(), CreateRequest{
		RunState:  rs,
		NodeID:    "node-a",
		Question:  "what happened?",
		Runner:    fr,
		Workspace: "/tmp/ws",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if res.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if !strings.HasPrefix(res.ID, "int-") {
		t.Errorf("ID should start with 'int-', got %q", res.ID)
	}
	if res.ForkedSessionID != "forked-sess-123" {
		t.Errorf("ForkedSessionID = %q, want %q", res.ForkedSessionID, "forked-sess-123")
	}
	if res.OrigSessionID != "orig-sess-456" {
		t.Errorf("OrigSessionID = %q, want %q", res.OrigSessionID, "orig-sess-456")
	}
	if res.Response != "forked response" {
		t.Errorf("Response = %q, want %q", res.Response, "forked response")
	}

	// Verify runner was called with Fork=true, Resume=true, correct SessionID
	if !fr.lastRequest.Fork {
		t.Error("expected Fork=true")
	}
	if !fr.lastRequest.Resume {
		t.Error("expected Resume=true")
	}
	if fr.lastRequest.SessionID != "orig-sess-456" {
		t.Errorf("runner SessionID = %q, want %q", fr.lastRequest.SessionID, "orig-sess-456")
	}
}

func TestManager_Ask(t *testing.T) {
	fr := &fakeRunner{result: runners.Result{
		Output:    "forked response",
		SessionID: "forked-sess-123",
	}}

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.SessionID = "orig-sess-456"
	})

	mgr := NewManager()
	created, err := mgr.Create(context.Background(), CreateRequest{
		RunState:  rs,
		NodeID:    "node-a",
		Question:  "initial question",
		Runner:    fr,
		Workspace: "/tmp/ws",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update the fake runner for the follow-up
	fr.result = runners.Result{Output: "follow-up answer"}

	askRes, err := mgr.Ask(context.Background(), created.ID, "follow-up question")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if askRes.Response != "follow-up answer" {
		t.Errorf("Response = %q, want %q", askRes.Response, "follow-up answer")
	}

	// Verify runner was called with Fork=false and the forked session ID
	if fr.lastRequest.Fork {
		t.Error("expected Fork=false on follow-up")
	}
	if fr.lastRequest.SessionID != "forked-sess-123" {
		t.Errorf("runner SessionID = %q, want forked session %q", fr.lastRequest.SessionID, "forked-sess-123")
	}
}

func TestManager_Ask_NotFound(t *testing.T) {
	mgr := NewManager()
	_, err := mgr.Ask(context.Background(), "nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for nonexistent interrogation")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to contain 'not found'", err.Error())
	}
}

func TestManager_Create_NoSession(t *testing.T) {
	fr := &fakeRunner{result: runners.Result{Output: "x"}}

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("node-a", func(n *state.NodeState) {
		// No SessionID set
	})

	mgr := NewManager()
	_, err := mgr.Create(context.Background(), CreateRequest{
		RunState:  rs,
		NodeID:    "node-a",
		Question:  "what happened?",
		Runner:    fr,
		Workspace: "/tmp/ws",
	})
	if err == nil {
		t.Fatal("expected error when node has no session")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("error = %q, want it to contain 'no session'", err.Error())
	}
}

func TestManager_List(t *testing.T) {
	fr := &fakeRunner{result: runners.Result{
		Output:    "response",
		SessionID: "forked-sess",
	}}

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.SessionID = "orig-sess"
	})

	mgr := NewManager()
	_, err := mgr.Create(context.Background(), CreateRequest{
		RunState:  rs,
		NodeID:    "node-a",
		Question:  "question",
		Runner:    fr,
		Workspace: "/tmp/ws",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries := mgr.List()
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].NodeID != "node-a" {
		t.Errorf("NodeID = %q, want %q", entries[0].NodeID, "node-a")
	}
	if entries[0].RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", entries[0].RunID, "run-1")
	}
}

func TestManager_Expire(t *testing.T) {
	fr := &fakeRunner{result: runners.Result{
		Output:    "response",
		SessionID: "forked-sess",
	}}

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.SessionID = "orig-sess"
	})

	mgr := NewManager()
	created, err := mgr.Create(context.Background(), CreateRequest{
		RunState:  rs,
		NodeID:    "node-a",
		Question:  "question",
		Runner:    fr,
		Workspace: "/tmp/ws",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Backdate the LastActive to trigger expiry
	mgr.mu.Lock()
	if entry, ok := mgr.sessions[created.ID]; ok {
		entry.LastActive = time.Now().Add(-31 * time.Minute)
	}
	mgr.mu.Unlock()

	mgr.Sweep()

	_, err = mgr.Ask(context.Background(), created.ID, "should fail")
	if err == nil {
		t.Fatal("expected error after expiry")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to contain 'not found'", err.Error())
	}
}

type slowFakeRunner struct {
	result      runners.Result
	delay       time.Duration
	mu          sync.Mutex
	callCount   int
	inflight    int
	maxInflight int
}

func (r *slowFakeRunner) Run(_ context.Context, req runners.Request, handler runners.LineHandler) (runners.Result, error) {
	r.mu.Lock()
	r.inflight++
	if r.inflight > r.maxInflight {
		r.maxInflight = r.inflight
	}
	r.mu.Unlock()

	time.Sleep(r.delay)

	r.mu.Lock()
	r.callCount++
	r.inflight--
	r.mu.Unlock()

	if handler != nil {
		handler(runners.Line{Stream: "stdout", Text: r.result.Output})
	}
	return r.result, nil
}

func TestManager_ConcurrentAsksAreSerialized(t *testing.T) {
	fr := &fakeRunner{result: runners.Result{
		Output:    "create response",
		SessionID: "forked-sess",
	}}

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.SessionID = "orig-sess"
	})

	mgr := NewManager()
	created, err := mgr.Create(context.Background(), CreateRequest{
		RunState: rs, NodeID: "node-a", Question: "Q1",
		Runner: fr, Workspace: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	slow := &slowFakeRunner{
		result: runners.Result{Output: "answer", SessionID: "forked-sess"},
		delay:  50 * time.Millisecond,
	}

	// Replace runner with slow version for follow-ups
	mgr.mu.Lock()
	mgr.sessions[created.ID].Runner = slow
	mgr.mu.Unlock()

	// Launch 3 concurrent asks
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mgr.Ask(context.Background(), created.ID, "concurrent question")
		}()
	}
	wg.Wait()

	slow.mu.Lock()
	count := slow.callCount
	maxInfl := slow.maxInflight
	slow.mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 calls, got %d", count)
	}
	if maxInfl != 1 {
		t.Errorf("expected max 1 concurrent call (serialized), got %d", maxInfl)
	}
}
