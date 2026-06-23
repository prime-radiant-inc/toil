package runners

import (
	"context"
	"testing"
	"time"
)

func TestShellRunnerTimesOutSlowProcess(t *testing.T) {
	runner := NewShellRunner(Config{
		TimeoutSec: 1,
	})

	start := time.Now()
	_, err := runner.Run(context.Background(), Request{
		Prompt: "sleep 30",
	}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timed-out process")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout did not fire: took %v", elapsed)
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	runner := NewShellRunner(Config{
		TimeoutSec: 1,
	})

	done := make(chan struct{})
	var elapsed time.Duration
	var runErr error

	go func() {
		start := time.Now()
		// Background child inherits stdout pipe; without process group kill,
		// the pipe stays open after the parent is killed, causing a hang.
		_, runErr = runner.Run(context.Background(), Request{
			Prompt: "sleep 300 & sleep 300",
		}, nil)
		elapsed = time.Since(start)
		close(done)
	}()

	select {
	case <-done:
		if runErr == nil {
			t.Fatal("expected error from timed-out process")
		}
		if elapsed > 5*time.Second {
			t.Fatalf("process group not killed promptly: took %v", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("test timed out: process group not killed, runCommand hung")
	}
}

func TestShellRunnerNoTimeoutWhenZero(t *testing.T) {
	runner := NewShellRunner(Config{
		TimeoutSec: 0,
	})

	_, err := runner.Run(context.Background(), Request{
		Prompt: "echo hello",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
