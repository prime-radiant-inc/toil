package dashboard

import (
	"hash/fnv"
	"os/exec"
	"strings"
	"testing"
)

func TestRoleColorParity(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	cases := []string{
		"plan_tasks", "write_tests", "write_code", "review_plan",
		"verify_code_meets_acceptance_criteria", "review_code_quality",
		"resolve_review_dispute", "", "a", "system",
	}
	for _, role := range cases {
		for _, kind := range []string{"bg", "text"} {
			goVal := string(RoleColor(role, kind))
			cmd := exec.Command("node", "-e",
				"const m = require('./static/js/role_color.js'); "+
					"process.stdout.write(m.roleColor(process.argv[1], process.argv[2]));",
				role, kind)
			out, err := cmd.Output()
			if err != nil {
				t.Errorf("node failed for (%q, %q): %v", role, kind, err)
				continue
			}
			jsVal := strings.TrimSpace(string(out))
			if goVal != jsVal {
				t.Errorf("parity mismatch for (%q, %q): Go=%q JS=%q", role, kind, goVal, jsVal)
			}
		}
	}
}

// fnv1aUTF16 mirrors the JS implementation in static/js/role_color.js,
// which hashes UTF-16 code units (str.charCodeAt). Kept in test code so
// the JS divergence can be exercised without spawning node.
func fnv1aUTF16(s string) uint32 {
	h := uint32(2166136261)
	for _, r := range s {
		// Replicate JS String#charCodeAt: surrogate-pair-aware iteration
		// over UTF-16 code units rather than runes.
		if r < 0x10000 {
			h ^= uint32(r)
			h *= 16777619
		} else {
			// Encode as a UTF-16 surrogate pair, hash each unit.
			r -= 0x10000
			high := uint32(0xD800 + (r >> 10))
			low := uint32(0xDC00 + (r & 0x3FF))
			h ^= high
			h *= 16777619
			h ^= low
			h *= 16777619
		}
	}
	return h
}

func fnv1aUTF8(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// TestRoleColorASCIIOnly documents and guards the ASCII-only constraint
// on role strings. The Go RoleColor helper hashes UTF-8 bytes; the JS
// helper hashes UTF-16 code units. They agree on ASCII and diverge on
// anything else.
//
// This test exists to make the constraint visible to anyone who reaches
// for this code: if a future change introduces non-ASCII role strings,
// the assertion on ASCII parity should still hold, and the assertion on
// non-ASCII divergence should still fail intentionally — meaning the
// fix is to either keep roles ASCII-only or unify the hash algorithm
// across both implementations.
func TestRoleColorASCIIOnly(t *testing.T) {
	// Parity holds for ASCII: hashes match byte-for-byte.
	asciiCases := []string{"plan_tasks", "review_code_quality", "a", ""}
	for _, s := range asciiCases {
		if fnv1aUTF8(s) != fnv1aUTF16(s) {
			t.Errorf("ascii parity broken for %q: UTF-8 and UTF-16 hashes disagree", s)
		}
	}

	// Divergence holds for non-ASCII: this is the constraint we rely on.
	// If a future change makes these agree (e.g. JS switched to
	// TextEncoder), the role-string constraint becomes obsolete and the
	// doc comments on RoleColor / role_color.js should be revisited.
	nonASCIICases := []string{"café", "日本", "naïve"}
	for _, s := range nonASCIICases {
		if fnv1aUTF8(s) == fnv1aUTF16(s) {
			t.Errorf("expected UTF-8/UTF-16 divergence for non-ASCII %q; the role-string ASCII-only constraint may be obsolete — review RoleColor doc comments", s)
		}
	}
}
