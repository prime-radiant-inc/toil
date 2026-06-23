package dashboard

import (
	"strings"
	"testing"
)

func TestRoleColorStability(t *testing.T) {
	a := RoleColor("plan_tasks", "bg")
	b := RoleColor("plan_tasks", "bg")
	if a != b {
		t.Errorf("RoleColor not deterministic: %q vs %q", a, b)
	}
}

func TestRoleColorFormat(t *testing.T) {
	got := string(RoleColor("plan_tasks", "bg"))
	if !strings.HasPrefix(got, "oklch(55% 0.15 ") {
		t.Errorf("bg format wrong: %q", got)
	}
	if !strings.HasSuffix(got, "deg)") {
		t.Errorf("bg suffix wrong: %q", got)
	}
}

func TestRoleColorBgVsText(t *testing.T) {
	bg := RoleColor("plan_tasks", "bg")
	text := RoleColor("plan_tasks", "text")
	if bg == text {
		t.Errorf("bg and text should differ: bg=%q text=%q", bg, text)
	}
}

func TestRoleColorDifferentRoles(t *testing.T) {
	a := RoleColor("plan_tasks", "bg")
	b := RoleColor("write_code", "bg")
	if a == b {
		t.Errorf("different roles should produce different colors: %q vs %q", a, b)
	}
}
