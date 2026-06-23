package dashboard

import (
	"strings"
	"testing"
)

func TestUnifiedDiff_AddRemoveReplace(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		want []string
	}{
		{
			name: "insert_one_line",
			old:  "a\nb\nc\n",
			new:  "a\nb\nX\nc\n",
			want: []string{" a", " b", "+X", " c"},
		},
		{
			name: "delete_one_line",
			old:  "a\nb\nc\n",
			new:  "a\nc\n",
			want: []string{" a", "-b", " c"},
		},
		{
			name: "replace_one_line",
			old:  "a\nOLD\nc\n",
			new:  "a\nNEW\nc\n",
			want: []string{" a", "-OLD", "+NEW", " c"},
		},
		{
			name: "empty_old",
			old:  "",
			new:  "hello\n",
			want: []string{"+hello"},
		},
		{
			name: "empty_new",
			old:  "bye\n",
			new:  "",
			want: []string{"-bye"},
		},
		{
			name: "identical",
			old:  "same\n",
			new:  "same\n",
			want: []string{" same"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UnifiedDiff(tc.old, tc.new)
			lastIdx := -1
			for _, sub := range tc.want {
				idx := strings.Index(got, sub)
				if idx < 0 {
					t.Fatalf("missing %q in diff:\n%s", sub, got)
				}
				if idx < lastIdx {
					t.Fatalf("out-of-order %q in diff:\n%s", sub, got)
				}
				lastIdx = idx
			}
		})
	}
}

func TestUnifiedDiff_PreservesTrailingNewlineHandling(t *testing.T) {
	got := UnifiedDiff("a\nb", "a\nb\nc")
	if !strings.Contains(got, "+c") {
		t.Fatalf("expected +c in diff, got:\n%s", got)
	}
}
