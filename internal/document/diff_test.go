package document

import (
	"strings"
	"testing"
)

func TestUnifiedDiff_SimpleChange(t *testing.T) {
	before := "a\nb\nc\nd\n"
	after := "a\nB\nc\nd\n"
	hunks := UnifiedDiff(before, after, 1)
	if len(hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d: %+v", len(hunks), hunks)
	}
	h := hunks[0]
	var minus, plus int
	for _, l := range h.Lines {
		if len(l) == 0 {
			continue
		}
		switch l[0] {
		case '-':
			minus++
		case '+':
			plus++
		}
	}
	if minus != 1 || plus != 1 {
		t.Fatalf("want 1 -/+ each, got -%d +%d (lines: %v)", minus, plus, h.Lines)
	}
}

func TestUnifiedDiff_PureAddition(t *testing.T) {
	before := "a\nb\n"
	after := "a\nb\nc\n"
	hunks := UnifiedDiff(before, after, 1)
	if len(hunks) == 0 {
		t.Fatalf("want at least 1 hunk")
	}
	var plus int
	for _, h := range hunks {
		for _, l := range h.Lines {
			if len(l) > 0 && l[0] == '+' {
				plus++
			}
		}
	}
	if plus != 1 {
		t.Fatalf("want 1 added line, got %d", plus)
	}
}

func TestUnifiedDiff_PureDeletion(t *testing.T) {
	before := "a\nb\nc\n"
	after := "a\nc\n"
	hunks := UnifiedDiff(before, after, 1)
	if len(hunks) == 0 {
		t.Fatalf("want at least 1 hunk")
	}
	var minus int
	for _, h := range hunks {
		for _, l := range h.Lines {
			if len(l) > 0 && l[0] == '-' {
				minus++
			}
		}
	}
	if minus != 1 {
		t.Fatalf("want 1 removed line, got %d", minus)
	}
}

func TestUnifiedDiff_Identical(t *testing.T) {
	s := "a\nb\nc\n"
	hunks := UnifiedDiff(s, s, 1)
	if len(hunks) != 0 {
		t.Fatalf("identical strings should produce no hunks, got %+v", hunks)
	}
}

func TestUnifiedDiff_ChangeLines_HaveProperPrefix(t *testing.T) {
	before := "a\nb\n"
	after := "a\nB\n"
	hunks := UnifiedDiff(before, after, 1)
	for _, h := range hunks {
		for _, l := range h.Lines {
			if len(l) == 0 {
				continue
			}
			if !strings.ContainsAny(string(l[0]), "+- ") {
				t.Fatalf("line prefix must be +, -, or space: %q", l)
			}
		}
	}
}
