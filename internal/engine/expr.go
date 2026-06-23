package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// EvalContext provides variables for expression evaluation.
type EvalContext struct {
	Decision string
	Status   string // "completed" or "failed"
	Message  string
	Resolve  func(path string) (any, error) // for node.X.data.Y paths
}

// IsExpression returns true if the string contains expression operators,
// indicating it should be parsed as an expression rather than a plain decision match.
func IsExpression(s string) bool {
	return strings.Contains(s, "==") ||
		strings.Contains(s, "!=") ||
		strings.Contains(s, ">=") ||
		strings.Contains(s, "<=") ||
		strings.Contains(s, "&&") ||
		strings.Contains(s, "||") ||
		// Single > or < but NOT inside => or <=
		containsSingleAngleBracket(s)
}

func containsSingleAngleBracket(s string) bool {
	for i, ch := range s {
		if ch == '>' && (i == 0 || s[i-1] != '=') && (i+1 >= len(s) || s[i+1] != '=') {
			return true
		}
		if ch == '<' && (i+1 >= len(s) || s[i+1] != '=') {
			return true
		}
	}
	return false
}

// EvalEdgeExpr evaluates an expression string against the given context.
// Returns true if the expression matches.
func EvalEdgeExpr(expr string, ctx *EvalContext) (bool, error) {
	tokens, err := tokenize(expr)
	if err != nil {
		return false, err
	}
	p := &parser{tokens: tokens, ctx: ctx}
	return p.parseOr()
}

// --- Tokenizer ---

type tokenKind int

const (
	tokIdent  tokenKind = iota // variable name or dotted path
	tokString                  // 'quoted string'
	tokNumber                  // integer or float
	tokEq                      // ==
	tokNeq                     // !=
	tokGte                     // >=
	tokLte                     // <=
	tokGt                      // >
	tokLt                      // <
	tokAnd                     // &&
	tokOr                      // ||
)

type token struct {
	kind tokenKind
	text string
}

func tokenize(s string) ([]token, error) {
	var tokens []token
	i := 0
	for i < len(s) {
		ch := s[i]

		// Skip whitespace
		if ch == ' ' || ch == '\t' {
			i++
			continue
		}

		// Two-character operators
		if i+1 < len(s) {
			pair := s[i : i+2]
			switch pair {
			case "==":
				tokens = append(tokens, token{tokEq, "=="})
				i += 2
				continue
			case "!=":
				tokens = append(tokens, token{tokNeq, "!="})
				i += 2
				continue
			case ">=":
				tokens = append(tokens, token{tokGte, ">="})
				i += 2
				continue
			case "<=":
				tokens = append(tokens, token{tokLte, "<="})
				i += 2
				continue
			case "&&":
				tokens = append(tokens, token{tokAnd, "&&"})
				i += 2
				continue
			case "||":
				tokens = append(tokens, token{tokOr, "||"})
				i += 2
				continue
			}
		}

		// Single-character operators
		if ch == '>' {
			tokens = append(tokens, token{tokGt, ">"})
			i++
			continue
		}
		if ch == '<' {
			tokens = append(tokens, token{tokLt, "<"})
			i++
			continue
		}

		// String literal
		if ch == '\'' {
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("unterminated string literal at position %d", i)
			}
			tokens = append(tokens, token{tokString, s[i+1 : i+1+end]})
			i = i + 1 + end + 1
			continue
		}

		// Number or identifier (including dotted paths)
		if isIdentStart(ch) || isDigit(ch) {
			start := i
			for i < len(s) && (isIdentChar(s[i]) || s[i] == '.') {
				i++
			}
			text := s[start:i]
			if isNumericLiteral(text) {
				tokens = append(tokens, token{tokNumber, text})
			} else {
				tokens = append(tokens, token{tokIdent, text})
			}
			continue
		}

		return nil, fmt.Errorf("unexpected character %q at position %d", ch, i)
	}
	return tokens, nil
}

func isNumericLiteral(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return false
	}
	// Make sure it's not a dotted path like "node.x.y"
	for _, ch := range s {
		if ch != '.' && !isDigit(byte(ch)) && ch != '-' {
			return false
		}
	}
	return true
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentChar(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch)
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// --- Parser (recursive descent) ---

type parser struct {
	tokens []token
	pos    int
	ctx    *EvalContext
}

func (p *parser) peek() (token, bool) {
	if p.pos >= len(p.tokens) {
		return token{}, false
	}
	return p.tokens[p.pos], true
}

func (p *parser) next() (token, bool) {
	t, ok := p.peek()
	if ok {
		p.pos++
	}
	return t, ok
}

// parseOr: andExpr ("||" andExpr)*
func (p *parser) parseOr() (bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokOr {
			break
		}
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		left = left || right
	}
	return left, nil
}

// parseAnd: compare ("&&" compare)*
func (p *parser) parseAnd() (bool, error) {
	left, err := p.parseCompare()
	if err != nil {
		return false, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokAnd {
			break
		}
		p.next()
		right, err := p.parseCompare()
		if err != nil {
			return false, err
		}
		left = left && right
	}
	return left, nil
}

// parseCompare: operand (op operand)?
func (p *parser) parseCompare() (bool, error) {
	left, err := p.resolveOperand()
	if err != nil {
		return false, err
	}

	t, ok := p.peek()
	if !ok {
		// Single operand — truthy check
		return isTruthy(left), nil
	}

	switch t.kind {
	case tokEq, tokNeq, tokGt, tokLt, tokGte, tokLte:
		p.next()
		right, err := p.resolveOperand()
		if err != nil {
			return false, err
		}
		return compare(left, t.kind, right), nil
	default:
		return isTruthy(left), nil
	}
}

func (p *parser) resolveOperand() (any, error) {
	t, ok := p.next()
	if !ok {
		return nil, fmt.Errorf("expected operand, got end of expression")
	}
	switch t.kind {
	case tokString:
		return t.text, nil
	case tokNumber:
		n, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q: %w", t.text, err)
		}
		return n, nil
	case tokIdent:
		return p.resolveIdent(t.text), nil
	default:
		return nil, fmt.Errorf("unexpected token %q in operand position", t.text)
	}
}

func (p *parser) resolveIdent(name string) any {
	switch name {
	case fieldDecision:
		return p.ctx.Decision
	case fieldStatus:
		return p.ctx.Status
	case fieldMessage:
		return p.ctx.Message
	}
	// Try path resolution
	if p.ctx.Resolve != nil {
		val, err := p.ctx.Resolve(name)
		if err == nil {
			return val
		}
	}
	return nil // unresolvable -> nil
}

func compare(left any, op tokenKind, right any) bool {
	// Try numeric comparison
	ln, lok := toFloat(left)
	rn, rok := toFloat(right)
	if lok && rok {
		switch op {
		case tokEq:
			return ln == rn
		case tokNeq:
			return ln != rn
		case tokGt:
			return ln > rn
		case tokLt:
			return ln < rn
		case tokGte:
			return ln >= rn
		case tokLte:
			return ln <= rn
		}
	}

	// Fall back to string comparison
	ls := fmt.Sprint(left)
	rs := fmt.Sprint(right)
	switch op {
	case tokEq:
		return ls == rs
	case tokNeq:
		return ls != rs
	case tokGt:
		return ls > rs
	case tokLt:
		return ls < rs
	case tokGte:
		return ls >= rs
	case tokLte:
		return ls <= rs
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != ""
	case float64:
		return val != 0
	case int:
		return val != 0
	}
	return true
}
