# idleagent

A small Go CLI that reports how long ago each background agent process
has been idle. Pulls process times from `/proc/<pid>/stat` (Linux) or
`ps -p <pid> -o etime,stat` (macOS), formats the idle time, and prints
one agent per line.

## Behavior

- Command: `idleagent` (no flags, no arguments).
- Reads a config file `~/.config/idleagent/agents.json` listing agent
  PIDs to monitor: `[{"name": "fetcher", "pid": 1234}, ...]`.
- For each agent: reads the process's last-activity timestamp (the
  most recent of the process's read/write times under the platform's
  conventions), computes elapsed time, and prints:
  `<name>\t<idle-time>` on stdout.
- Idle-time format is human-relative:
  - `active` (< 30 seconds)
  - `idle 5 minutes`
  - `idle 2 hours`
  - `idle 3 days`
- Sorts output by least-idle first.
- If the config file is missing, prints a clear error to stderr and
  exits with code 1.

## Implementation requirements

- Language: Go (1.21+).
- Single module, single `main.go`. No external dependencies.
- Uses the standard library `os`, `os/exec`, `time`, `encoding/json`,
  `path/filepath`.

## Tests

- `main_test.go` with table-driven tests for the idle-time formatter.
  Cover each unit boundary (active, minutes, hours, days). Tests
  should be deterministic and not depend on wall-clock time.

## Deliverables

- `go.mod` with module name `idleagent`.
- `main.go` containing the CLI entrypoint and the formatter.
- `main_test.go`.
- `README.md` describing usage and the config file format.
- `go test ./...` must pass.
- `go build` must succeed.
