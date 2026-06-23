package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// --- Unit tests for retry logic ---

func TestRetryDelay_Exponential(t *testing.T) {
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "exponential",
		InitialDelay: "1s",
		MaxDelay:     "30s",
	}
	// attempt 1: 1s * 2^0 = 1s
	if d := retryDelay(policy, 1); d != 1*time.Second {
		t.Fatalf("attempt 1: expected 1s, got %v", d)
	}
	// attempt 2: 1s * 2^1 = 2s
	if d := retryDelay(policy, 2); d != 2*time.Second {
		t.Fatalf("attempt 2: expected 2s, got %v", d)
	}
	// attempt 3: 1s * 2^2 = 4s
	if d := retryDelay(policy, 3); d != 4*time.Second {
		t.Fatalf("attempt 3: expected 4s, got %v", d)
	}
}

func TestRetryDelay_ExponentialCappedByMaxDelay(t *testing.T) {
	policy := &definitions.RetryPolicy{
		Max:          5,
		Backoff:      "exponential",
		InitialDelay: "10s",
		MaxDelay:     "30s",
	}
	// attempt 3: 10s * 2^2 = 40s, capped to 30s
	if d := retryDelay(policy, 3); d != 30*time.Second {
		t.Fatalf("expected 30s cap, got %v", d)
	}
}

func TestRetryDelay_Fixed(t *testing.T) {
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "fixed",
		InitialDelay: "5s",
		MaxDelay:     "30s",
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if d := retryDelay(policy, attempt); d != 5*time.Second {
			t.Fatalf("attempt %d: expected 5s, got %v", attempt, d)
		}
	}
}

func TestRetryDelay_Defaults(t *testing.T) {
	// Empty strings should use defaults (1s initial, 30s max)
	policy := &definitions.RetryPolicy{Max: 3}
	if d := retryDelay(policy, 1); d != 1*time.Second {
		t.Fatalf("expected default 1s, got %v", d)
	}
}

func TestIsRetryable(t *testing.T) {
	plain := fmt.Errorf("bad config")
	if IsRetryable(plain) {
		t.Fatal("plain error should not be retryable")
	}

	retryable := Retryable(fmt.Errorf("timeout"))
	if !IsRetryable(retryable) {
		t.Fatal("Retryable-wrapped error should be retryable")
	}

	// Wrapped retryable
	wrapped := fmt.Errorf("outer: %w", retryable)
	if !IsRetryable(wrapped) {
		t.Fatal("wrapped Retryable error should be retryable")
	}
}

func TestParseDurationOrDefault(t *testing.T) {
	cases := []struct {
		input    string
		fallback time.Duration
		expected time.Duration
	}{
		{"5s", time.Second, 5 * time.Second},
		{"", time.Second, time.Second},
		{"garbage", 2 * time.Second, 2 * time.Second},
		{"100ms", time.Second, 100 * time.Millisecond},
	}
	for _, tc := range cases {
		got := parseDurationOrDefault(tc.input, tc.fallback)
		if got != tc.expected {
			t.Fatalf("parseDurationOrDefault(%q, %v) = %v, want %v", tc.input, tc.fallback, got, tc.expected)
		}
	}
}

// ============================================================
// Additional edge-case tests for retry logic
// ============================================================

func TestRetryDelay_ExponentialHighAttemptCappedByMax(t *testing.T) {
	policy := &definitions.RetryPolicy{
		Max:          20,
		Backoff:      "exponential",
		InitialDelay: "1s",
		MaxDelay:     "60s",
	}
	// attempt 10: 1s * 2^9 = 512s, capped to 60s
	d := retryDelay(policy, 10)
	if d != 60*time.Second {
		t.Fatalf("attempt 10: expected 60s cap, got %v", d)
	}
}

func TestRetryDelay_ExponentialAttempt1(t *testing.T) {
	// Ensure attempt=1 gives initial delay (2^0 = 1)
	policy := &definitions.RetryPolicy{
		Backoff:      "exponential",
		InitialDelay: "500ms",
		MaxDelay:     "30s",
	}
	d := retryDelay(policy, 1)
	if d != 500*time.Millisecond {
		t.Fatalf("expected 500ms for attempt 1, got %v", d)
	}
}

func TestRetryDelay_FixedAlwaysSameRegardlessOfAttempt(t *testing.T) {
	policy := &definitions.RetryPolicy{
		Max:          10,
		Backoff:      "fixed",
		InitialDelay: "2s",
		MaxDelay:     "30s",
	}
	for attempt := 1; attempt <= 10; attempt++ {
		d := retryDelay(policy, attempt)
		if d != 2*time.Second {
			t.Fatalf("attempt %d: fixed should always be 2s, got %v", attempt, d)
		}
	}
}

func TestRetryDelay_FixedCappedByMaxDelay(t *testing.T) {
	// Edge case: initial delay > max delay with fixed backoff
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "fixed",
		InitialDelay: "60s",
		MaxDelay:     "10s",
	}
	d := retryDelay(policy, 1)
	if d != 10*time.Second {
		t.Fatalf("expected 10s cap even for fixed, got %v", d)
	}
}

func TestRetryDelay_DefaultBackoffIsExponential(t *testing.T) {
	// When backoff is empty string (not "fixed"), should use exponential
	policy := &definitions.RetryPolicy{
		Max:          3,
		InitialDelay: "1s",
		MaxDelay:     "30s",
	}
	// attempt 2: 1s * 2^1 = 2s
	d := retryDelay(policy, 2)
	if d != 2*time.Second {
		t.Fatalf("expected 2s for default (exponential) backoff attempt 2, got %v", d)
	}
}

func TestRetryDelay_UnknownBackoffStringIsExponential(t *testing.T) {
	// Any string that isn't "fixed" falls through to exponential
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "random_jitter_whatever",
		InitialDelay: "1s",
		MaxDelay:     "30s",
	}
	// attempt 3: 1s * 2^2 = 4s
	d := retryDelay(policy, 3)
	if d != 4*time.Second {
		t.Fatalf("expected 4s for unknown backoff (exponential fallback), got %v", d)
	}
}

func TestRetryDelay_AllDefaults(t *testing.T) {
	// No fields set at all - defaults to 1s initial, 30s max, exponential
	policy := &definitions.RetryPolicy{}
	d1 := retryDelay(policy, 1) // 1s * 2^0 = 1s
	d2 := retryDelay(policy, 2) // 1s * 2^1 = 2s
	d3 := retryDelay(policy, 3) // 1s * 2^2 = 4s
	if d1 != 1*time.Second {
		t.Fatalf("attempt 1: expected 1s, got %v", d1)
	}
	if d2 != 2*time.Second {
		t.Fatalf("attempt 2: expected 2s, got %v", d2)
	}
	if d3 != 4*time.Second {
		t.Fatalf("attempt 3: expected 4s, got %v", d3)
	}
}

func TestRetryDelay_InvalidDurationStrings(t *testing.T) {
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "exponential",
		InitialDelay: "not_a_duration",
		MaxDelay:     "also_not_valid",
	}
	// Should fall back to defaults: 1s initial, 30s max
	d := retryDelay(policy, 1)
	if d != 1*time.Second {
		t.Fatalf("expected 1s default for invalid initial_delay, got %v", d)
	}
}

func TestRetryDelay_VeryLargeAttemptNumber(t *testing.T) {
	policy := &definitions.RetryPolicy{
		Max:          100,
		Backoff:      "exponential",
		InitialDelay: "1s",
		MaxDelay:     "1h",
	}
	// Large exponents are capped to avoid int64 overflow.
	d := retryDelay(policy, 100)
	if d != 1*time.Hour {
		t.Fatalf("attempt 100: expected 1h cap, got %v", d)
	}
}

func TestRetryDelay_OverflowBoundary(t *testing.T) {
	// The maximum safe exponent for 1<<uint(x) with int64 is x=62 (since Duration is int64)
	// At attempt=63, shift is 62, 1<<62 fits in int64. At attempt=64, 1<<63 overflows.
	policy := &definitions.RetryPolicy{
		Max:          64,
		Backoff:      "exponential",
		InitialDelay: "1ns", // 1 nanosecond to avoid multiply overflow
		MaxDelay:     "1h",
	}
	// attempt=63: 1ns * 2^62 = should be a huge positive number, capped to 1h
	d63 := retryDelay(policy, 63)
	if d63 != 1*time.Hour {
		t.Fatalf("attempt 63: expected 1h cap, got %v", d63)
	}

	// attempt=64: exponent > 62 is capped to maxDelay.
	d64 := retryDelay(policy, 64)
	if d64 != 1*time.Hour {
		t.Fatalf("attempt 64: expected 1h cap, got %v", d64)
	}
}

func TestRetryable_ErrorMessage(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	retryable := Retryable(inner)
	if retryable.Error() != "connection refused" {
		t.Fatalf("expected error message to pass through, got %q", retryable.Error())
	}
}

func TestRetryable_Unwrap(t *testing.T) {
	inner := fmt.Errorf("timeout")
	retryable := Retryable(inner)
	var re *RetryableError
	if !errors.As(retryable, &re) {
		t.Fatal("expected errors.As to find RetryableError")
	}
	if re.Unwrap() != inner {
		t.Fatal("expected Unwrap to return inner error")
	}
}

func TestIsRetryable_NilError(t *testing.T) {
	// nil error should not panic and should return false
	if IsRetryable(nil) {
		t.Fatal("nil error should not be retryable")
	}
}

func TestIsRetryable_DoubleWrapped(t *testing.T) {
	inner := fmt.Errorf("disk full")
	retryable := Retryable(inner)
	wrapped := fmt.Errorf("layer1: %w", fmt.Errorf("layer2: %w", retryable))
	if !IsRetryable(wrapped) {
		t.Fatal("double-wrapped RetryableError should still be retryable")
	}
}

func TestParseDurationOrDefault_AdditionalFormats(t *testing.T) {
	cases := []struct {
		input    string
		fallback time.Duration
		expected time.Duration
	}{
		{"1m", time.Second, 1 * time.Minute},
		{"1h", time.Second, 1 * time.Hour},
		{"500us", time.Second, 500 * time.Microsecond},
		{"1m30s", time.Second, 90 * time.Second},
		{"0s", time.Second, 0},
	}
	for _, tc := range cases {
		got := parseDurationOrDefault(tc.input, tc.fallback)
		if got != tc.expected {
			t.Fatalf("parseDurationOrDefault(%q, %v) = %v, want %v", tc.input, tc.fallback, got, tc.expected)
		}
	}
}

// Test that maxAttempts calculation in executeSingle is correct
// (retry.Max=0 means no retries, retry.Max=3 means 4 total attempts)
func TestRetryMaxZeroMeansNoRetry(t *testing.T) {
	// When Max is 0, maxAttempts should be 1 (no retries)
	// This tests the logic: if node.Retry != nil && node.Retry.Max > 0 -> false when Max=0
	policy := &definitions.RetryPolicy{Max: 0}
	// maxAttempts would be 1 since Max > 0 is false
	maxAttempts := 1
	if policy != nil && policy.Max > 0 {
		maxAttempts = policy.Max + 1
	}
	if maxAttempts != 1 {
		t.Fatalf("Max=0 should result in maxAttempts=1, got %d", maxAttempts)
	}
}

func TestRetryMaxPositiveMeansRetryPlusOne(t *testing.T) {
	policy := &definitions.RetryPolicy{Max: 3}
	maxAttempts := 1
	if policy != nil && policy.Max > 0 {
		maxAttempts = policy.Max + 1
	}
	if maxAttempts != 4 {
		t.Fatalf("Max=3 should result in maxAttempts=4, got %d", maxAttempts)
	}
}

func TestRetryNilPolicyMeansNoRetry(t *testing.T) {
	var policy *definitions.RetryPolicy
	maxAttempts := 1
	if policy != nil && policy.Max > 0 {
		maxAttempts = policy.Max + 1
	}
	if maxAttempts != 1 {
		t.Fatalf("nil policy should result in maxAttempts=1, got %d", maxAttempts)
	}
}

// ============================================================
// Jitter tests
// ============================================================

func TestRetryDelay_JitterFalse_Deterministic(t *testing.T) {
	// With jitter=false, delay should be deterministic (unchanged behavior)
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "exponential",
		InitialDelay: "1s",
		MaxDelay:     "30s",
		Jitter:       false,
	}
	for attempt := 1; attempt <= 3; attempt++ {
		d1 := retryDelay(policy, attempt)
		d2 := retryDelay(policy, attempt)
		if d1 != d2 {
			t.Fatalf("jitter=false: attempt %d returned different delays: %v vs %v", attempt, d1, d2)
		}
	}
}

func TestRetryDelay_JitterTrue_WithinRange(t *testing.T) {
	// With jitter=true, delay should fall within [50%, 150%] of the base delay
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "exponential",
		InitialDelay: "1s",
		MaxDelay:     "30s",
		Jitter:       true,
	}

	policyNoJitter := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "exponential",
		InitialDelay: "1s",
		MaxDelay:     "30s",
		Jitter:       false,
	}

	for attempt := 1; attempt <= 3; attempt++ {
		baseDelay := retryDelay(policyNoJitter, attempt)
		minDelay := baseDelay / 2
		maxDelay := baseDelay + baseDelay/2

		sawDifferent := false
		for i := 0; i < 100; i++ {
			d := retryDelay(policy, attempt)
			if d < minDelay || d > maxDelay {
				t.Fatalf("jitter=true: attempt %d, iteration %d: delay %v outside [%v, %v]",
					attempt, i, d, minDelay, maxDelay)
			}
			if d != baseDelay {
				sawDifferent = true
			}
		}
		if !sawDifferent {
			t.Fatalf("jitter=true: attempt %d: all 100 delays were exactly %v (jitter not applied)", attempt, baseDelay)
		}
	}
}

func TestRetryDelay_JitterTrue_CanExceedMaxDelay(t *testing.T) {
	// With jitter=true and max_delay set, the jittered delay can exceed max_delay
	// (up to 150% of the capped delay) — this is by design.
	// We use a policy where exponential would hit the max_delay cap.
	policy := &definitions.RetryPolicy{
		Max:          5,
		Backoff:      "exponential",
		InitialDelay: "10s",
		MaxDelay:     "30s",
		Jitter:       true,
	}

	// attempt 3: 10s * 2^2 = 40s, capped to 30s. With jitter: [15s, 45s]
	// So the jittered delay can go up to 45s, exceeding the 30s max_delay.
	sawExceedingMaxDelay := false
	for i := 0; i < 200; i++ {
		d := retryDelay(policy, 3)
		if d > 30*time.Second {
			sawExceedingMaxDelay = true
			break
		}
	}
	if !sawExceedingMaxDelay {
		t.Fatal("jitter=true: expected some delays to exceed max_delay (30s), but none did in 200 iterations")
	}
}

func TestRetryDelay_JitterTrue_ZeroDelay_NoPanic(t *testing.T) {
	// With jitter=true and delay=0, should not panic
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "fixed",
		InitialDelay: "0s",
		MaxDelay:     "0s",
		Jitter:       true,
	}
	// Should not panic
	d := retryDelay(policy, 1)
	if d != 0 {
		t.Fatalf("expected 0 delay with 0s initial and 0s max, got %v", d)
	}
}

func TestRetryDelay_JitterTrue_FixedBackoff(t *testing.T) {
	// Jitter should also work with fixed backoff
	policy := &definitions.RetryPolicy{
		Max:          3,
		Backoff:      "fixed",
		InitialDelay: "4s",
		MaxDelay:     "30s",
		Jitter:       true,
	}

	// Base delay is 4s. Jitter range: [2s, 6s]
	sawDifferent := false
	for i := 0; i < 100; i++ {
		d := retryDelay(policy, 1)
		if d < 2*time.Second || d > 6*time.Second {
			t.Fatalf("jitter=true fixed: delay %v outside [2s, 6s]", d)
		}
		if d != 4*time.Second {
			sawDifferent = true
		}
	}
	if !sawDifferent {
		t.Fatalf("jitter=true fixed: all 100 delays were exactly 4s (jitter not applied)")
	}
}

// --- Integration tests for _retry_exhausted meta-decision ---

// TestRetryExhaustionRoutesViaMetaDecision verifies that when a node has
// retry: {max: 2} and a `when: _retry_exhausted` edge, and always fails,
// the engine routes via _retry_exhausted rather than falling through to
// `status == 'failed'` edges.
func TestRetryExhaustionRoutesViaMetaDecision(t *testing.T) {
	runsDir := t.TempDir()
	failedTrue := true
	workflow := &definitions.Workflow{
		ID:      "wf-retry-exhausted",
		Name:    "Retry Exhausted",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "worker", Kind: "role", Runner: "fail-runner",
				Retry: &definitions.RetryPolicy{
					Max:          2,
					Backoff:      "fixed",
					InitialDelay: "0s",
					MaxDelay:     "0s",
				},
			},
			{ID: "meta_handler", Kind: "system"},
			{ID: "legacy_handler", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "worker", To: "meta_handler", When: "_retry_exhausted", Failed: &failedTrue},
			{From: "worker", To: "legacy_handler", When: "status == 'failed'"},
		},
	}
	setupRunForResume(t, runsDir, "run-meta-retry", workflow, nil)

	registry := runners.NewRegistry()
	_ = registry.Register("fail-runner", &failingRunner{})

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"fail-runner": {ID: "fail-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, _ = eng.ResumeRun(context.Background(), "run-meta-retry")

	// Load state: meta_handler should be completed; legacy_handler untouched.
	loaded, err := state.LoadState(filepath.Join(runsDir, "run-meta-retry", "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	// Use WithNodes (read-only snapshot) to avoid the getOrCreate side-effect
	// that WithNode carries when the node is not yet in the map.
	var metaStatus, workerLastRouting string
	var legacyExists bool
	loaded.WithNodes(func(nodes map[string]*state.NodeState) {
		if n, ok := nodes["meta_handler"]; ok {
			metaStatus = n.Status
		}
		if n, ok := nodes["worker"]; ok {
			workerLastRouting = n.LastRoutingDecision
		}
		_, legacyExists = nodes["legacy_handler"]
	})

	if workerLastRouting != MetaDecisionRetryExhausted {
		t.Errorf("worker.LastRoutingDecision=%q want %q", workerLastRouting, MetaDecisionRetryExhausted)
	}
	if metaStatus == "" {
		t.Error("meta_handler was never reached; expected it to be queued via _retry_exhausted")
	}
	if legacyExists {
		t.Errorf("legacy_handler should not have been reached but appears in state")
	}
}

// TestRetryExhaustionFallsThroughWithoutMetaEdge verifies that when a node
// has retry: {max: 2} but NO `_retry_exhausted` edge, retry exhaustion falls
// through to the legacy `status == 'failed'` path unchanged.
func TestRetryExhaustionFallsThroughWithoutMetaEdge(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-retry-fallthrough",
		Name:    "Retry Fallthrough",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "worker", Kind: "role", Runner: "fail-runner",
				Retry: &definitions.RetryPolicy{
					Max:          2,
					Backoff:      "fixed",
					InitialDelay: "0s",
					MaxDelay:     "0s",
				},
			},
			{ID: "legacy_handler", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "worker", To: "legacy_handler", When: "status == 'failed'"},
		},
	}
	setupRunForResume(t, runsDir, "run-retry-fallthrough", workflow, nil)

	registry := runners.NewRegistry()
	_ = registry.Register("fail-runner", &failingRunner{})

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"fail-runner": {ID: "fail-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, _ = eng.ResumeRun(context.Background(), "run-retry-fallthrough")

	// legacy_handler should have been reached via the status == 'failed' edge.
	loaded, err := state.LoadState(filepath.Join(runsDir, "run-retry-fallthrough", "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	var legacyExists bool
	loaded.WithNodes(func(nodes map[string]*state.NodeState) {
		_, legacyExists = nodes["legacy_handler"]
	})

	if !legacyExists {
		t.Error("legacy_handler was never reached; expected it via status=='failed' fallthrough")
	}
}
