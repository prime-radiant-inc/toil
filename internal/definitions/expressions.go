package definitions

import (
	"regexp"
	"strings"
)

// ExprRef is one parsed ${...} reference inside a value string.
type ExprRef struct {
	Namespace string // input, workflow_input, node, env, run, tree
	Path      string // remainder after the namespace dot — may contain dots
	Required  bool   // true if the expression ended with !
	Raw       string // the original ${...} substring, useful for error messages
}

// Expression namespace prefixes — the closed set of valid ${<ns>.X}
// roots. KnownNamespaces enumerates them; comparison sites across the
// package reference these constants.
const (
	nsInput         = "input"
	nsWorkflowInput = "workflow_input"
	nsNode          = "node"
	nsEnv           = "env"
	nsRun           = "run"
	nsTree          = "tree"
)

// KnownNamespaces is the closed set of valid namespace prefixes.
var KnownNamespaces = []string{nsInput, nsWorkflowInput, nsNode, nsEnv, nsRun, nsTree}

// IsKnownNamespace reports whether ns is in KnownNamespaces.
func IsKnownNamespace(ns string) bool {
	for _, k := range KnownNamespaces {
		if k == ns {
			return true
		}
	}
	return false
}

// SupportedNodeFields is the closed set of fields addressable via
// ${node.<id>.<field>}. It is the single source of truth shared by the
// engine resolver (runtime, internal/engine/resolver.go) and load-time
// expression validation here — keeping both from drifting as the
// NodeState surface grows. Order matches the resolver's dispatch switch.
var SupportedNodeFields = []string{
	"decision",
	"message",
	"artifacts",
	"data",
	"session_id",
	"tags",
	"status",
	"attempts",
	"last_routing_decision",
	"loop_iterations",
}

// IsSupportedNodeField reports whether field is in SupportedNodeFields.
func IsSupportedNodeField(field string) bool {
	for _, f := range SupportedNodeFields {
		if f == field {
			return true
		}
	}
	return false
}

// exprPattern matches ${...}. Go regexp has no lookbehind, so $${...
// escape is filtered post-match by checking the preceding character.
var exprPattern = regexp.MustCompile(`\$\{([^}]*)\}`)

// FindExpressionRefs returns every ${...} reference in s, EXCLUDING
// $${... escape sequences. Returns nil if none found. Refs whose
// namespace prefix is not in KnownNamespaces are still returned (caller
// decides what to do — typically a load-time error).
func FindExpressionRefs(s string) []ExprRef {
	matches := exprPattern.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return nil
	}
	var refs []ExprRef
	for _, m := range matches {
		start := m[0]
		// Skip if preceded by another '$' (the escape form $${...).
		if start > 0 && s[start-1] == '$' {
			continue
		}
		inner := s[m[2]:m[3]]
		raw := s[m[0]:m[1]]
		ref := ExprRef{Raw: raw}
		if strings.HasSuffix(inner, "!") {
			ref.Required = true
			inner = inner[:len(inner)-1]
		}
		dot := strings.Index(inner, ".")
		if dot < 0 {
			// No namespace separator — likely a malformed expression.
			// Treat the whole thing as namespace; path is empty. Caller
			// can flag.
			ref.Namespace = inner
		} else {
			ref.Namespace = inner[:dot]
			ref.Path = inner[dot+1:]
		}
		refs = append(refs, ref)
	}
	return refs
}
