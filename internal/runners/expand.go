package runners

import (
	"os"
	"strings"
)

// ExpandArgsWithEnv returns a copy of args with environment variables expanded
// using the provided env map. Supports ${VAR:-default} syntax for fallback
// values. The env map is the sole source — no implicit os.Getenv fallback.
func ExpandArgsWithEnv(args []string, env map[string]string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = os.Expand(arg, func(key string) string {
			name, def, hasDef := strings.Cut(key, ":-")
			if val, ok := env[name]; ok {
				return val
			}
			if hasDef {
				return def
			}
			return ""
		})
	}
	return out
}
