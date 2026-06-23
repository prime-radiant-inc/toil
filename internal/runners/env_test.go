package runners

import (
	"sort"
	"testing"
)

func TestMergeEnv_BaseOnly(t *testing.T) {
	base := []string{"HOME=/home/user", "PATH=/usr/bin"}
	result := mergeEnv(base)

	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}

	sort.Strings(result)
	if result[0] != "HOME=/home/user" {
		t.Fatalf("result[0] = %q", result[0])
	}
	if result[1] != "PATH=/usr/bin" {
		t.Fatalf("result[1] = %q", result[1])
	}
}

func TestMergeEnv_OverrideExisting(t *testing.T) {
	base := []string{"HOME=/home/user", "PATH=/usr/bin"}
	overrides := map[string]string{"PATH": "/usr/local/bin"}

	result := mergeEnv(base, overrides)

	env := envToMap(result)
	if env["PATH"] != "/usr/local/bin" {
		t.Fatalf("PATH = %q, want /usr/local/bin", env["PATH"])
	}
	if env["HOME"] != "/home/user" {
		t.Fatalf("HOME = %q, want /home/user", env["HOME"])
	}
}

func TestMergeEnv_AddNew(t *testing.T) {
	base := []string{"HOME=/home/user"}
	overrides := map[string]string{"EDITOR": "vim"}

	result := mergeEnv(base, overrides)

	env := envToMap(result)
	if env["EDITOR"] != "vim" {
		t.Fatalf("EDITOR = %q, want vim", env["EDITOR"])
	}
	if env["HOME"] != "/home/user" {
		t.Fatalf("HOME should be preserved")
	}
}

func TestMergeEnv_MultipleOverrideMaps(t *testing.T) {
	base := []string{"A=1"}
	first := map[string]string{"A": "2", "B": "2"}
	second := map[string]string{"A": "3", "C": "3"}

	result := mergeEnv(base, first, second)

	env := envToMap(result)
	if env["A"] != "3" {
		t.Fatalf("A = %q, want 3 (last override wins)", env["A"])
	}
	if env["B"] != "2" {
		t.Fatalf("B = %q, want 2", env["B"])
	}
	if env["C"] != "3" {
		t.Fatalf("C = %q, want 3", env["C"])
	}
}

func TestMergeEnv_SkipsMalformedEntries(t *testing.T) {
	base := []string{"GOOD=value", "BADENTRY", "ALSO=fine"}
	result := mergeEnv(base)

	env := envToMap(result)
	if _, ok := env["BADENTRY"]; ok {
		t.Fatal("malformed entry should be skipped")
	}
	if len(env) != 2 {
		t.Fatalf("len = %d, want 2", len(env))
	}
}

func TestMergeEnv_EmptyBase(t *testing.T) {
	overrides := map[string]string{"KEY": "val"}
	result := mergeEnv(nil, overrides)

	env := envToMap(result)
	if env["KEY"] != "val" {
		t.Fatalf("KEY = %q, want val", env["KEY"])
	}
}

func TestMergeEnv_ValueWithEquals(t *testing.T) {
	base := []string{"URL=http://host:8080/path?a=1&b=2"}
	result := mergeEnv(base)

	env := envToMap(result)
	if env["URL"] != "http://host:8080/path?a=1&b=2" {
		t.Fatalf("URL = %q, value with = signs should be preserved", env["URL"])
	}
}

func TestMergeEnv_EmptyValue(t *testing.T) {
	base := []string{"EMPTY="}
	result := mergeEnv(base)

	env := envToMap(result)
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Fatalf("EMPTY should be present with empty value, got ok=%v v=%q", ok, v)
	}
}

func TestEnvMapFromSlice(t *testing.T) {
	slice := []string{"A=1", "B=hello=world", "MALFORMED"}
	m := envMapFromSlice(slice)
	if m["A"] != "1" {
		t.Errorf("A: got %q", m["A"])
	}
	if m["B"] != "hello=world" {
		t.Errorf("B: got %q", m["B"])
	}
	if _, ok := m["MALFORMED"]; ok {
		t.Error("should skip malformed entries")
	}
}

func TestResolveRunnerEnv_RequestOverridesConfig(t *testing.T) {
	config := Config{
		Args: []string{"--model", "${MODEL:-default-model}"},
		Env:  map[string]string{"MODEL": "config-model"},
	}
	requestEnv := map[string]string{"MODEL": "request-model"}

	env := resolveRunnerEnv(config, requestEnv)

	if env.Args[1] != "request-model" {
		t.Errorf("expected request-model to override, got %q", env.Args[1])
	}
}

func envToMap(env []string) map[string]string {
	return envMapFromSlice(env)
}
