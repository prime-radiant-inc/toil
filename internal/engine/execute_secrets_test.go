package engine

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestMergeSecretsIntoEnv(t *testing.T) {
	env := map[string]string{
		"PROJECT_DIR": "/some/project",
		"SOME_VAR":    "value",
	}
	secrets := map[string]string{
		"GITHUB_TOKEN": "ghp_test123",
		"AWS_SECRET":   "wJalrXU",
	}

	mergeSecretsIntoEnv(env, secrets)

	if env["GITHUB_TOKEN"] != "ghp_test123" {
		t.Fatalf("expected GITHUB_TOKEN in env, got %q", env["GITHUB_TOKEN"])
	}
	if env["AWS_SECRET"] != "wJalrXU" {
		t.Fatalf("expected AWS_SECRET in env, got %q", env["AWS_SECRET"])
	}
	// Original env preserved
	if env["PROJECT_DIR"] != "/some/project" {
		t.Fatalf("expected PROJECT_DIR preserved, got %q", env["PROJECT_DIR"])
	}
}

func TestFailFastMissingSecret(t *testing.T) {
	rs := state.NewRunState("run-1", "wf", nil)
	// Empty Secrets map - GITHUB_TOKEN not available
	rs.Secrets = map[string]string{}

	inputs := map[string]any{
		"secret_keys": []any{"GITHUB_TOKEN"},
	}

	err := checkRequiredSecrets(rs, inputs)
	if err == nil {
		t.Fatal("expected error for missing required secret")
	}
	if got := err.Error(); !strings.Contains(got, "GITHUB_TOKEN") {
		t.Fatalf("expected error to mention GITHUB_TOKEN, got %q", got)
	}
}

func TestPreDispatchSecretFailureRecordsNodeState(t *testing.T) {
	rs := state.NewRunState("run-1", "wf", nil)
	rs.Secrets = map[string]string{} // GITHUB_TOKEN not available

	dir := t.TempDir()
	logPath := dir + "/events.jsonl"
	logger, err := state.NewLoggerWithStdout(logPath, io.Discard)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	inputs := map[string]any{
		"secret_keys": []any{"GITHUB_TOKEN"},
	}

	// Simulate the pre-dispatch path: checkRequiredSecrets fails, node state
	// should still be recorded as failed with error message.
	secretErr := checkRequiredSecrets(rs, inputs)
	if secretErr == nil {
		t.Fatal("expected error for missing required secret")
	}
	recordPreDispatchFailure(rs, logger, "run-1", "check_delivery", secretErr)

	node := rs.Node("check_delivery")
	if node.Status != statusFailed {
		t.Fatalf("expected node status %q, got %q", statusFailed, node.Status)
	}
	if !strings.Contains(node.Error, "GITHUB_TOKEN") {
		t.Fatalf("expected node error to mention GITHUB_TOKEN, got %q", node.Error)
	}

	// Verify a node_failed event was emitted.
	_ = logger.Close()
	events, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read events: %v", readErr)
	}
	if !strings.Contains(string(events), `"node_failed"`) {
		t.Fatalf("expected node_failed event in log, got:\n%s", events)
	}
	if !strings.Contains(string(events), "check_delivery") {
		t.Fatalf("expected node ID in event log, got:\n%s", events)
	}
}

func TestMarkNodeFailedSetsError(t *testing.T) {
	rs := state.NewRunState("run-1", "wf", nil)

	dir := t.TempDir()
	logPath := dir + "/events.jsonl"
	logger, err := state.NewLoggerWithStdout(logPath, io.Discard)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	markNodeFailed(rs, logger, "run-1", "test_node", time.Now(), fmt.Errorf("merge conflict on main"))

	node := rs.Node("test_node")
	if node.Status != "failed" {
		t.Fatalf("expected status failed, got %q", node.Status)
	}
	if node.Error != "merge conflict on main" {
		t.Fatalf("expected Error = %q, got %q", "merge conflict on main", node.Error)
	}
}
