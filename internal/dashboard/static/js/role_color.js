// FNV-1a hash → OKLCH color. Parity with internal/dashboard/rolecolor.go.
//
// Role strings MUST be ASCII. This implementation hashes UTF-16 code
// units (charCodeAt) while the Go side hashes UTF-8 bytes; the two
// agree for ASCII input and diverge for anything else. ASCII-only is
// enforced upstream because role strings originate from YAML node IDs,
// which are ASCII snake_case by convention. See TestRoleColorASCIIOnly
// in internal/dashboard/rolecolor_parity_test.go for the regression
// guard that pins this constraint.
function fnv1a(str) {
  let h = 2166136261; // FNV-1a 32-bit offset basis
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 16777619); // FNV-1a 32-bit prime
  }
  return h >>> 0; // coerce to unsigned 32-bit
}

function roleColor(role, kind) {
  const hue = fnv1a(role) % 360;
  if (kind === "bg") return `oklch(55% 0.15 ${hue}deg)`;
  if (kind === "text") return `oklch(42% 0.18 ${hue}deg)`;
  return `oklch(50% 0.10 ${hue}deg)`;
}

if (typeof window !== "undefined") {
  window.roleColor = roleColor;
}
if (typeof module !== "undefined" && module.exports) {
  module.exports = { roleColor, fnv1a };
}
