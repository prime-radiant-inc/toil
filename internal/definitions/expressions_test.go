package definitions

import (
	"reflect"
	"sort"
	"testing"
)

func TestFindExpressionRefs(t *testing.T) {
	cases := []struct {
		in   string
		want []ExprRef
	}{
		{"${workflow_input.task!}", []ExprRef{{Namespace: "workflow_input", Path: "task", Required: true, Raw: "${workflow_input.task!}"}}},
		{"${input.x}", []ExprRef{{Namespace: "input", Path: "x", Raw: "${input.x}"}}},
		{"hello ${node.a.message} from ${env.HOST}", []ExprRef{
			{Namespace: "node", Path: "a.message", Raw: "${node.a.message}"},
			{Namespace: "env", Path: "HOST", Raw: "${env.HOST}"},
		}},
		{"price is $${notinterpolated}", nil}, // escaped — not a ref
		{"literal value", nil},
	}
	for _, tc := range cases {
		got := FindExpressionRefs(tc.in)
		sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
		sort.Slice(tc.want, func(i, j int) bool { return tc.want[i].Path < tc.want[j].Path })
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FindExpressionRefs(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestIsKnownNamespace(t *testing.T) {
	for _, ns := range []string{"input", "workflow_input", "node", "env", "run", "tree"} {
		if !IsKnownNamespace(ns) {
			t.Errorf("IsKnownNamespace(%q) = false, want true", ns)
		}
	}
	for _, ns := range []string{"foo", "Input", "", "INPUT"} {
		if IsKnownNamespace(ns) {
			t.Errorf("IsKnownNamespace(%q) = true, want false", ns)
		}
	}
}
