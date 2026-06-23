package dashboard

import (
	"fmt"
	"hash/fnv"
	"html/template"
)

// RoleColor returns a CSS OKLCH color string derived from the role
// string. kind is "bg" (avatar background) or "text" (breadcrumb text).
// Same role string always yields the same color. Algorithm parity is
// guaranteed against the JS helper in static/js/role_color.js.
//
// Returns template.CSS so that html/template embeds the value into
// inline style attributes without re-sanitizing the oklch(...) call.
//
// Role strings MUST be ASCII. The hash computes over raw byte values,
// which diverge between Go (UTF-8) and JS (UTF-16 code units) for
// non-ASCII input — server-rendered and SSE-injected avatars for the
// same role would then receive different colors. ASCII-only is enforced
// at the application layer because role strings originate from YAML
// node IDs, which are ASCII snake_case by convention. See
// TestRoleColorASCIIOnly in rolecolor_parity_test.go for the divergence
// regression guard.
func RoleColor(role, kind string) template.CSS {
	h := fnv.New32a()
	_, _ = h.Write([]byte(role))
	hue := int(h.Sum32() % 360)
	switch kind {
	case "bg":
		return template.CSS(fmt.Sprintf("oklch(55%% 0.15 %ddeg)", hue))
	case fieldText:
		return template.CSS(fmt.Sprintf("oklch(42%% 0.18 %ddeg)", hue))
	default:
		return template.CSS(fmt.Sprintf("oklch(50%% 0.10 %ddeg)", hue))
	}
}
