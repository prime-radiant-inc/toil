.PHONY: build build-toil build-semantic-port build-tgwm test test-faults fmt lint setup serve serve-daemon dev

BIN_DIR := bin
TOIL_BIN := $(BIN_DIR)/toil
SEMANTIC_PORT_BIN := $(BIN_DIR)/semantic_port
TGWM_BIN := $(BIN_DIR)/tgwm
TEST_PACKAGES := $(shell go list -e ./... | grep -v '/runs/.*/artifacts')

# `make build` produces both binaries the engine needs at runtime. The
# engine shells out to bin/tgwm at $TOIL_ROOT/bin/tgwm — building only
# toil leaves bin/tgwm stale, which silently breaks shell nodes that
# depend on tgwm flag/CLI surface added since the last build-tgwm.
build: build-toil build-tgwm

build-toil:
	@mkdir -p $(BIN_DIR)
	go build -o $(TOIL_BIN) ./cmd/toil

build-semantic-port:
	@mkdir -p $(BIN_DIR)
	go build -o $(SEMANTIC_PORT_BIN) ./cmd/semantic_port

build-tgwm:
	@mkdir -p $(BIN_DIR)
	go build -o $(TGWM_BIN) ./cmd/tgwm

test:
	go test $(TEST_PACKAGES)

# `make test-faults` runs the engine observability fault-injection
# tests in isolation, with -v so each path is named in the output.
# These tests deliberately fire the diagnostic events that PRI-1570,
# PRI-1573, PRI-1575, and PRI-1576 added — they verify the events
# fire end-to-end through the real engine, not just the helpers.
#
# See docs/testing/fault-injection.md for what each test exercises and
# the gaps the harness does NOT yet cover.
test-faults:
	go test -v -run 'TestExecuteSingle_PreDispatchResolveInputsFailure|TestObservabilityIntegration|TestResolveInterrogationWorkspace|TestRunWithResumeFallback_EmitsDegradedEvent|TestForEachCascade|TestExecuteSubworkflow_FailureSetsErrorOnParent|TestExecuteSubworkflow_OrchestratorLevelFailure|TestBuildFailureContext_PreDispatchFailure|TestSummarizeForOriginatingFailure_NilWhenEmpty' ./internal/engine/ ./internal/api/

fmt:
	gofumpt -w .

lint:
	golangci-lint run ./...

setup:
	brew install lefthook golangci-lint gofumpt
	lefthook install

SERF_DIR := $(shell cd ../serf && pwd)

serve: build
	set -a && . $(SERF_DIR)/.env && set +a && PATH="$(SERF_DIR):$$PATH" $(TOIL_BIN) serve --addr :8080

serve-daemon: build
	set -a && . $(SERF_DIR)/.env && set +a && PATH="$(SERF_DIR):$$PATH" $(TOIL_BIN) serve --addr :8080 --daemon

dev: build
	set -a && . $(SERF_DIR)/.env && set +a && PATH="$(SERF_DIR):$$PATH" $(TOIL_BIN) serve --addr :8080 --daemon
	tail -f runs/server.log
