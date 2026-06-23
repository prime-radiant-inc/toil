package runners

import (
	"os"
	"strings"
)

// envMapFromSlice converts a []string{"KEY=VALUE",...} slice to a map.
func envMapFromSlice(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok {
			m[k] = v
		}
	}
	return m
}

// resolvedEnv holds the merged environment and expanded args for a runner
// invocation.
type resolvedEnv struct {
	Slice []string // KEY=VALUE list for exec.Cmd.Env
	Args  []string // config args with ${VAR:-default} expanded
}

// resolveRunnerEnv merges process + config + request env, expands config
// args against the merged result, and returns both. Every runner uses this
// to build its command — the shared implementation prevents merge-order bugs.
func resolveRunnerEnv(config Config, requestEnv map[string]string) resolvedEnv {
	mergedMap := mergeEnvToMap(os.Environ(), config.Env, requestEnv)
	return resolvedEnv{
		Slice: envSliceFromMap(mergedMap),
		Args:  ExpandArgsWithEnv(config.Args, mergedMap),
	}
}

// mergeEnvToMap merges a base env slice with override maps into a single map.
// Later overrides take precedence.
func mergeEnvToMap(base []string, overrides ...map[string]string) map[string]string {
	values := envMapFromSlice(base)
	for _, override := range overrides {
		for key, value := range override {
			values[key] = value
		}
	}
	return values
}

func envSliceFromMap(m map[string]string) []string {
	s := make([]string, 0, len(m))
	for key, value := range m {
		s = append(s, key+"="+value)
	}
	return s
}

// mergeEnv merges a base env slice with override maps into a KEY=VALUE slice.
func mergeEnv(base []string, overrides ...map[string]string) []string {
	return envSliceFromMap(mergeEnvToMap(base, overrides...))
}
