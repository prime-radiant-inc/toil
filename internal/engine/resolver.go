package engine

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"primeradiant.com/toil/internal/definitions"
)

var (
	ErrInputNotFound      = errors.New("input not found")
	ErrNodeOutputNotFound = errors.New("node output not found")
	ErrNodeDataNotFound   = errors.New("node data not found")
)

var templateExprPattern = regexp.MustCompile(`\$\{([^{}]+)\}`)

type RunContext struct {
	RunID          string
	Inputs         map[string]any
	Outputs        map[string]NodeOutput
	OptionalInputs map[string]bool
	// Tree is the optional cross-run resolver for `tree.*` expressions.
	// nil when the context doesn't have tree access (unit tests, synthetic
	// contexts). See tree_resolver.go for semantics.
	Tree TreeResolver
	// Env is the snapshot of environment variables this run was
	// created with. Populated by the engine at dispatch time from
	// runState.Env. Resolver dispatches ${env.X} reads against this
	// map.
	Env map[string]string
}

func (ctx *RunContext) Resolve(expression string) (any, error) {
	trimmed := strings.TrimSpace(expression)
	// Single-expression short-circuit: a value that is exactly
	// "${expr}" or "${expr!}" (no surrounding text) preserves the
	// resolved value's native Go type rather than stringifying.
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") {
		inner := trimmed[2 : len(trimmed)-1]
		// Reject nested ${ or stray } — these are template strings.
		if !strings.ContainsAny(inner, "${}") {
			// Determine the bare inner expression (strip trailing ! for the
			// namespace prefix check).
			bareInner := inner
			if strings.HasSuffix(bareInner, "!") {
				bareInner = strings.TrimSpace(bareInner[:len(bareInner)-1])
			}
			// Only short-circuit if the inner expression has a known namespace
			// prefix. Unknown-prefix expressions fall through to the template
			// path, which handles them via hasKnownNamespacePrefix guards
			// (preserving backwards compat for e.g. ${ENV_VAR} literals).
			if hasKnownNamespacePrefix(bareInner) {
				required := false
				if strings.HasSuffix(inner, "!") {
					required = true
					inner = bareInner
				}
				value, err := ctx.Resolve(inner)
				if err != nil {
					if required {
						return nil, fmt.Errorf("unresolved required reference: %s: %w", inner, err)
					}
					if isOptionalResolveError(err) {
						return nil, nil
					}
					return nil, err
				}
				if value == nil && required {
					return nil, fmt.Errorf("unresolved required reference: %s (resolved to nil)", inner)
				}
				return value, nil
			}
		}
	}

	if strings.HasPrefix(expression, "run.") {
		path := strings.TrimPrefix(expression, "run.")
		switch path {
		case "id":
			if strings.TrimSpace(ctx.RunID) == "" {
				return nil, fmt.Errorf("run id not set")
			}
			return ctx.RunID, nil
		default:
			return nil, fmt.Errorf("unknown run field: %s", path)
		}
	}

	if strings.HasPrefix(expression, "tree.") {
		if ctx.Tree == nil {
			return nil, fmt.Errorf("tree expressions not available: no TreeResolver configured on RunContext")
		}
		return ctx.resolveTreeExpression(strings.TrimPrefix(expression, "tree."))
	}

	if strings.HasPrefix(expression, "env.") {
		key := strings.TrimPrefix(expression, "env.")
		if !validIdentifier(key) {
			return nil, fmt.Errorf("invalid env reference %q: name must be an identifier", expression)
		}
		v, ok := ctx.Env[key]
		if !ok {
			return nil, fmt.Errorf("env %s not set", key)
		}
		return v, nil
	}

	if strings.HasPrefix(expression, "workflow_input.") {
		path := strings.TrimPrefix(expression, "workflow_input.")
		parts := strings.Split(path, ".")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid workflow_input expression: %s", expression)
		}
		key := parts[0]
		value, ok := ctx.Inputs[key]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrInputNotFound, key)
		}
		if len(parts) > 1 {
			return resolveInputPath(value, parts[1:])
		}
		return value, nil
	}

	if strings.HasPrefix(expression, "input.") {
		path := strings.TrimPrefix(expression, "input.")
		parts := strings.Split(path, ".")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid input expression: %s", expression)
		}
		key := parts[0]
		value, ok := ctx.Inputs[key]
		if !ok {
			if ctx.OptionalInputs != nil && ctx.OptionalInputs[key] {
				return nil, nil
			}
			return nil, fmt.Errorf("%w: %s", ErrInputNotFound, key)
		}
		if len(parts) > 1 {
			nested, err := resolveInputPath(value, parts[1:])
			if err != nil {
				if ctx.OptionalInputs != nil && ctx.OptionalInputs[key] {
					return nil, nil
				}
				return nil, err
			}
			return nested, nil
		}
		return value, nil
	}

	if strings.HasPrefix(expression, "node.") {
		parts := strings.Split(expression, ".")
		if len(parts) < 2 || parts[1] == "" {
			return nil, fmt.Errorf("invalid node expression: %s", expression)
		}
		nodeID := parts[1]
		output, ok := ctx.Outputs[nodeID]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNodeOutputNotFound, nodeID)
		}
		if len(parts) == 2 {
			// `node.X` (no field) returns the full output as a map so
			// downstream consumers can pull decision, message, and data
			// together without destructuring in YAML.
			return map[string]any{
				fieldDecision:            output.Decision,
				fieldMessage:             output.Message,
				fieldArtifacts:           output.Artifacts,
				fieldData:                output.Data,
				fieldSessionID:           output.SessionID,
				fieldTags:                output.Tags,
				fieldStatus:              output.Status,
				fieldAttempts:            output.Attempts,
				fieldLastRoutingDecision: output.LastRoutingDecision,
				fieldLoopIterations:      output.LoopIterations,
			}, nil
		}
		field := parts[2]
		switch field {
		case fieldDecision:
			return output.Decision, nil
		case fieldMessage:
			return output.Message, nil
		case fieldArtifacts:
			return output.Artifacts, nil
		case fieldSessionID:
			return output.SessionID, nil
		case fieldTags:
			return output.Tags, nil
		case fieldStatus:
			return output.Status, nil
		case fieldAttempts:
			return output.Attempts, nil
		case fieldLastRoutingDecision:
			return output.LastRoutingDecision, nil
		case fieldLoopIterations:
			return output.LoopIterations, nil
		case fieldData:
			if len(parts) == 3 {
				return output.Data, nil
			}
			return resolveMap(output.Data, parts[3:])
		default:
			return nil, fmt.Errorf("unknown node field: %s (supported: %s)", field, strings.Join(definitions.SupportedNodeFields, ", "))
		}
	}

	if strings.Contains(expression, "${") {
		rendered, replaced, err := ctx.resolveTemplateExpressions(expression)
		if err != nil {
			return nil, err
		}
		if replaced {
			return rendered, nil
		}
	}

	return expression, nil
}

// resolveTreeExpression dispatches `tree.<projection>...` after the
// "tree." prefix has been stripped. Currently only one projection is
// defined:
//
//	tree.tagged.<tag> — nodes across the run tree whose Tags
//	                    list contains <tag>. Tag is a workflow-
//	                    declared label materialized onto NodeState
//	                    at emit time.
//
// Unknown projections return a descriptive error — silent nil would
// hide expression typos.
// PopulateEnv copies env into ctx.Env so dispatch-time ${env.X}
// reads work. Safe to call repeatedly; later calls overwrite the
// matching keys.
func (ctx *RunContext) PopulateEnv(env map[string]string) {
	if ctx.Env == nil {
		ctx.Env = make(map[string]string, len(env))
	}
	for k, v := range env {
		ctx.Env[k] = v
	}
}

func (ctx *RunContext) resolveTreeExpression(path string) (any, error) {
	parts := strings.SplitN(path, ".", 2)
	head := parts[0]
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}
	switch head {
	case "tagged":
		if rest == "" {
			return nil, fmt.Errorf("tree.tagged.<tag> requires a tag name (e.g. tree.tagged.override)")
		}
		if strings.ContainsRune(rest, '.') {
			return nil, fmt.Errorf("tree.tagged.<tag> only supports a single-segment tag (got %q)", rest)
		}
		return ctx.Tree.FindNodesByTag(rest)
	default:
		return nil, fmt.Errorf("unknown tree projection %q (supported: tagged.<tag>)", head)
	}
}

func (ctx *RunContext) resolveTemplateExpressions(expression string) (string, bool, error) {
	const sentinel = "\x00\x00ESCAPED_DOLLAR\x00\x00"
	pre := strings.ReplaceAll(expression, "$${", sentinel)

	matches := templateExprPattern.FindAllStringSubmatchIndex(pre, -1)
	if len(matches) == 0 {
		// Even with no matches, sentinel-only inputs still need post-processing.
		if strings.Contains(pre, sentinel) {
			return strings.ReplaceAll(pre, sentinel, "${"), true, nil
		}
		return "", false, nil
	}

	var builder strings.Builder
	last := 0
	replaced := false

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		start, end := match[0], match[1]
		exprStart, exprEnd := match[2], match[3]
		builder.WriteString(pre[last:start])

		inner := strings.TrimSpace(pre[exprStart:exprEnd])
		if !hasKnownNamespacePrefix(inner) {
			builder.WriteString(pre[start:end])
			last = end
			continue
		}

		value, err := ctx.Resolve(inner)
		if err != nil {
			return "", false, err
		}
		if value == nil {
			// Template null renders as empty string (NOT "<nil>")
			last = end
			replaced = true
			continue
		}
		fmt.Fprint(&builder, value)
		last = end
		replaced = true
	}

	builder.WriteString(pre[last:])
	final := strings.ReplaceAll(builder.String(), sentinel, "${")
	return final, replaced || strings.Contains(expression, "$${"), nil
}

func resolveMap(value any, path []string) (any, error) {
	return walkMapPath(value, path, ErrNodeDataNotFound)
}

func resolveInputPath(value any, path []string) (any, error) {
	return walkMapPath(value, path, ErrInputNotFound)
}

func validIdentifier(s string) bool {
	if s == "" {
		return false
	}
	if !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentChar(s[i]) {
			return false
		}
	}
	return true
}

func hasKnownNamespacePrefix(s string) bool {
	for _, ns := range []string{"run.", "input.", "workflow_input.", "node.", "env.", "tree."} {
		if strings.HasPrefix(s, ns) {
			return true
		}
	}
	return false
}

func walkMapPath(value any, path []string, sentinel error) (any, error) {
	current := value
	for _, segment := range path {
		if current == nil {
			return nil, fmt.Errorf("%w: %s", sentinel, segment)
		}
		rv := reflect.ValueOf(current)
		switch rv.Kind() {
		case reflect.Map:
			mapped, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: %s", sentinel, segment)
			}
			next, ok := mapped[segment]
			if !ok {
				return nil, fmt.Errorf("%w: %s", sentinel, segment)
			}
			current = next
		case reflect.Slice, reflect.Array:
			idx, err := strconv.Atoi(segment)
			if err != nil {
				return nil, fmt.Errorf("cannot use non-integer %q as slice index", segment)
			}
			if idx < 0 {
				return nil, fmt.Errorf("negative slice index %q not supported", segment)
			}
			if idx >= rv.Len() {
				return nil, fmt.Errorf("slice index %d out of bounds (len=%d)", idx, rv.Len())
			}
			current = rv.Index(idx).Interface()
		default:
			return nil, fmt.Errorf("%w: %s", sentinel, segment)
		}
	}
	return current, nil
}
