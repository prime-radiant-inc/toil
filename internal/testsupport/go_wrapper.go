package testsupport

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

const wrapperEnv = "TOIL_GO_WRAPPER"

var installGoWrapperOnce sync.Once

func InstallGoWrapper() {
	installGoWrapperOnce.Do(func() {
		if os.Getenv(wrapperEnv) == "1" {
			return
		}

		realGo, err := exec.LookPath("go")
		if err != nil {
			return
		}

		dir, err := os.MkdirTemp("", "toil-go-wrapper-")
		if err != nil {
			return
		}

		script := fmt.Sprintf(`#!/bin/sh
real_go=%q
if [ "$1" = "run" ]; then
	tmp_stdout=$(mktemp)
	tmp_stderr=$(mktemp)
	"$real_go" "$@" >"$tmp_stdout" 2>"$tmp_stderr"
	status=$?
	cat "$tmp_stdout"
	if [ "$status" -ne 0 ]; then
		last_line=$(tail -n 1 "$tmp_stderr")
		case "$last_line" in
			exit\ status\ *)
				sed '$d' "$tmp_stderr" >&2
				;;
			*)
				cat "$tmp_stderr" >&2
				;;
		esac
	else
		cat "$tmp_stderr" >&2
	fi
	rm -f "$tmp_stdout" "$tmp_stderr"
	exit "$status"
fi
exec "$real_go" "$@"
`, realGo)

		path := filepath.Join(dir, "go")
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			return
		}

		currentPath := os.Getenv("PATH")
		if currentPath == "" {
			currentPath = dir
		} else {
			currentPath = dir + string(os.PathListSeparator) + currentPath
		}
		if err := os.Setenv("PATH", currentPath); err != nil {
			return
		}
		_ = os.Setenv(wrapperEnv, "1")
	})
}
