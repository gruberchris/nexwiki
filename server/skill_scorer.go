package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// This file holds story 11 (Phase B2): the versioned scorer registry and the
// sandboxed scorer execution model behind the real eval pipeline.
//
// THE REGISTRY: every eval case carries an optional `scorer` field. The value
// is either the name of a built-in scorer ("exact" — the default, so an empty
// field scores exact-match too) or an inline scorer expression. Custom scorers
// are therefore pure, self-describing source text: the same string is compiled
// the same way everywhere (upload gate, S0 baseline, iteration gating, the
// pre-live simulation), which is what makes gate decisions reproducible from
// stored data.
//
// THE SANDBOX: a scorer may not spawn processes, touch the network, read
// files, keep state, or call anything outside a fixed function allowlist. It
// is a tiny expression language over the three case-bound string variables
// (`input`, `expected`, `output` — `output` is the skill/candidate body being
// scored) with string/number literals, arithmetic, comparisons, boolean
// operators, and eight pure functions. The parser is a recursive-descent
// reader over a bounded token stream with bounded nesting depth, and the
// compiled tree is statically type-checked, so evaluation is TOTAL: a scorer
// that compiles can never fail, panic, allocate unboundedly, or do anything
// but compute a number from its three inputs. Anything else — unknown
// function, unknown variable, type mismatch, unterminated string, too long,
// too deep — is refused at upload with a fix-it error naming the scorer and
// the offset. There is no escape hatch: no eval, no exec, no regexp, no
// imports.
//
// SCORING SHAPE: a per-case score is the expression value clamped into
// [0,1]; a split score is the mean over the split's per-case scores, rounded
// to 4 decimals exactly like RBestScore and the audit's ValidationScore, so
// gate compares stay exact.
//
// VERSIONING: scorer versions are derived, never claimed. "v0" is the story
// 03 structural heuristic (SkillAuditScorerVersion, the fallback when a skill
// has no stored eval set). "v1" is eval-backed scoring under the default
// exact-match scorer alone; any custom scorer set folds a hash of the sorted
// sources into the version ("v1-<hash12>"), so the version bumps itself the
// moment the scorer set changes — and because the version is mixed into the
// eval hash (skill_eval.go), an upload with a changed scorer set changes the
// eval hash, the audit stamp, and the report stamp together.

// Scorer version constant. EvalScorerVersionBase is the eval-backed scorer
// version when the eval set uses only the built-in default (exact-match);
// custom scorer sets fold a hash into the version below. The structural
// fallback scorer version (SkillAuditScorerVersion, "v0") lives in
// skill_audit.go beside the audit records it stamps.
const (
	EvalScorerVersionBase = "v1"
)

// Scorer name recognized as the built-in exact-match scorer.
const defaultScorerName = "exact"

// Sandbox bounds. An expression longer or deeper than this is refused at
// upload — a scorer is a formula, not a program.
const (
	scorerExprMaxBytes = 512
	scorerMaxTokens    = 128
	scorerMaxDepth     = 24
	// scorerVersionHashLen is the hex length folded into a custom scorer
	// version ("v1-<12 hex>").
	scorerVersionHashLen = 12
)

// builtinScorerNames is the complete built-in registry. Everything else must
// parse as an expression or the upload is refused.
var builtinScorerNames = map[string]string{
	defaultScorerName: "built-in exact match: 1 when the output equals the expected answer (trimmed), else 0",
}

// scorerAllowlistNames renders the function allowlist for refusal messages —
// sorted so the list is deterministic.
var scorerAllowlistNames = []string{
	"abs(x)", "contains(haystack, needle)", "len(s)", "lower(s)",
	"max(x, y)", "min(x, y)", "overlap(a, b)", "trim(s)",
}

// scorerVal is one evaluator value. Booleans are numbers (1/0) so the whole
// language has exactly two types and every comparison result is arithmetic.
type scorerVal struct {
	kind scorerKind
	num  float64
	str  string
}

type scorerKind int

const (
	scorerNum scorerKind = iota
	scorerStr
)

func (v scorerVal) truthy() bool {
	return v.kind == scorerNum && v.num != 0
}

// scorerNode is one compiled AST node: eval is total and pure, kind is its
// statically checked type.
type scorerNode interface {
	eval(input, expected, output string) scorerVal
	kind() scorerKind
}

type scorerNumNode struct{ v float64 }

func (n scorerNumNode) eval(_, _, _ string) scorerVal { return scorerVal{kind: scorerNum, num: n.v} }
func (n scorerNumNode) kind() scorerKind              { return scorerNum }

type scorerStrNode struct{ v string }

func (n scorerStrNode) eval(_, _, _ string) scorerVal { return scorerVal{kind: scorerStr, str: n.v} }
func (n scorerStrNode) kind() scorerKind              { return scorerStr }

// scorerVarNode reads one of the three case-bound strings.
type scorerVarNode struct{ name string }

func (n scorerVarNode) eval(input, expected, output string) scorerVal {
	switch n.name {
	case "input":
		return scorerVal{kind: scorerStr, str: input}
	case "expected":
		return scorerVal{kind: scorerStr, str: expected}
	default: // "output"
		return scorerVal{kind: scorerStr, str: output}
	}
}
func (n scorerVarNode) kind() scorerKind { return scorerStr }

// scorerCallNode is one allowlisted pure function call. fn receives the
// already-evaluated arguments; argKinds is the statically checked signature.
type scorerCallNode struct {
	name     string
	args     []scorerNode
	argKinds []scorerKind
	ret      scorerKind
	fn       func(args []scorerVal) scorerVal
}

func (n *scorerCallNode) eval(input, expected, output string) scorerVal {
	vals := make([]scorerVal, len(n.args))
	for i, a := range n.args {
		vals[i] = a.eval(input, expected, output)
	}
	return n.fn(vals)
}
func (n *scorerCallNode) kind() scorerKind { return n.ret }

// scorerBinOpNode is one binary operator with both operand kinds checked.
type scorerBinOpNode struct {
	op       string
	l, r     scorerNode
	operandK scorerKind
}

func (n *scorerBinOpNode) eval(input, expected, output string) scorerVal {
	l := n.l.eval(input, expected, output)
	r := n.r.eval(input, expected, output)
	num := func(v scorerVal) float64 { return v.num }
	switch n.op {
	case "||":
		if l.truthy() || r.truthy() {
			return scorerVal{kind: scorerNum, num: 1}
		}
		return scorerVal{kind: scorerNum, num: 0}
	case "&&":
		if l.truthy() && r.truthy() {
			return scorerVal{kind: scorerNum, num: 1}
		}
		return scorerVal{kind: scorerNum, num: 0}
	case "==":
		if l.kind == scorerStr {
			if l.str == r.str {
				return scorerVal{kind: scorerNum, num: 1}
			}
			return scorerVal{kind: scorerNum, num: 0}
		}
		if l.num == r.num {
			return scorerVal{kind: scorerNum, num: 1}
		}
		return scorerVal{kind: scorerNum, num: 0}
	case "!=":
		if l.kind == scorerStr {
			if l.str != r.str {
				return scorerVal{kind: scorerNum, num: 1}
			}
			return scorerVal{kind: scorerNum, num: 0}
		}
		if l.num != r.num {
			return scorerVal{kind: scorerNum, num: 1}
		}
		return scorerVal{kind: scorerNum, num: 0}
	case ">":
		return numBool(num(l) > num(r))
	case "<":
		return numBool(num(l) < num(r))
	case ">=":
		return numBool(num(l) >= num(r))
	case "<=":
		return numBool(num(l) <= num(r))
	case "+":
		return scorerVal{kind: scorerNum, num: num(l) + num(r)}
	case "-":
		return scorerVal{kind: scorerNum, num: num(l) - num(r)}
	case "*":
		return scorerVal{kind: scorerNum, num: num(l) * num(r)}
	case "/":
		// Total by design: a zero divisor yields 0 instead of an error or
		// NaN, so a compiled scorer can never fail at evaluation time.
		if r.num == 0 {
			return scorerVal{kind: scorerNum, num: 0}
		}
		return scorerVal{kind: scorerNum, num: num(l) / num(r)}
	}
	// Unreachable: the parser only produces the operators above.
	return scorerVal{kind: scorerNum, num: 0}
}
func (n *scorerBinOpNode) kind() scorerKind { return scorerNum }

// scorerUnaryNode is `!x` or `-x` (numeric operand, statically checked).
type scorerUnaryNode struct {
	op      string
	x       scorerNode
	operand scorerKind
}

func (n *scorerUnaryNode) eval(input, expected, output string) scorerVal {
	v := n.x.eval(input, expected, output)
	if n.op == "!" {
		if v.truthy() {
			return scorerVal{kind: scorerNum, num: 0}
		}
		return scorerVal{kind: scorerNum, num: 1}
	}
	return scorerVal{kind: scorerNum, num: -v.num}
}
func (n *scorerUnaryNode) kind() scorerKind { return scorerNum }

func numBool(b bool) scorerVal {
	if b {
		return scorerVal{kind: scorerNum, num: 1}
	}
	return scorerVal{kind: scorerNum, num: 0}
}

// scorerFn are the eight sandboxed pure functions. Every one is a plain
// string/number computation over its arguments — no environment, no state,
// no I/O by construction.
var scorerFn = map[string]struct {
	args    []scorerKind
	ret     scorerKind
	sigText string
	apply   func(args []scorerVal) scorerVal
}{
	"overlap": {[]scorerKind{scorerStr, scorerStr}, scorerNum, "overlap(a, b)",
		func(args []scorerVal) scorerVal {
			// Fraction of a's word tokens that appear in b's token set — the
			// same tokenization the v0 baseline used, now under an operator's
			// control. 0 when a carries no tokens.
			a := evalWordTokens(args[0].str)
			if len(a) == 0 {
				return scorerVal{kind: scorerNum, num: 0}
			}
			b := evalWordTokens(args[1].str)
			hit := 0
			for tok := range a {
				if b[tok] {
					hit++
				}
			}
			return scorerVal{kind: scorerNum, num: float64(hit) / float64(len(a))}
		}},
	"contains": {[]scorerKind{scorerStr, scorerStr}, scorerNum, "contains(haystack, needle)",
		func(args []scorerVal) scorerVal {
			if strings.Contains(args[0].str, args[1].str) {
				return scorerVal{kind: scorerNum, num: 1}
			}
			return scorerVal{kind: scorerNum, num: 0}
		}},
	"lower": {[]scorerKind{scorerStr}, scorerStr, "lower(s)",
		func(args []scorerVal) scorerVal { return scorerVal{kind: scorerStr, str: strings.ToLower(args[0].str)} }},
	"trim": {[]scorerKind{scorerStr}, scorerStr, "trim(s)",
		func(args []scorerVal) scorerVal {
			return scorerVal{kind: scorerStr, str: strings.TrimSpace(args[0].str)}
		}},
	"len": {[]scorerKind{scorerStr}, scorerNum, "len(s)",
		func(args []scorerVal) scorerVal { return scorerVal{kind: scorerNum, num: float64(len(args[0].str))} }},
	"min": {[]scorerKind{scorerNum, scorerNum}, scorerNum, "min(x, y)",
		func(args []scorerVal) scorerVal {
			return scorerVal{kind: scorerNum, num: math.Min(args[0].num, args[1].num)}
		}},
	"max": {[]scorerKind{scorerNum, scorerNum}, scorerNum, "max(x, y)",
		func(args []scorerVal) scorerVal {
			return scorerVal{kind: scorerNum, num: math.Max(args[0].num, args[1].num)}
		}},
	"abs": {[]scorerKind{scorerNum}, scorerNum, "abs(x)",
		func(args []scorerVal) scorerVal { return scorerVal{kind: scorerNum, num: math.Abs(args[0].num)} }},
}

// scorerToken is one lexed token with its source offset for fix-it errors.
type scorerToken struct {
	off  int
	kind string // "num", "str", "ident", "op"
	text string
	num  float64
}

// lexScorer tokenizes the expression source. Rejects anything that is not
// part of the fixed grammar, with the offset so a refusal can point at it.
func lexScorer(src string) ([]scorerToken, error) {
	var toks []scorerToken
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t':
			i++
			continue
		case c >= '0' && c <= '9':
			start := i
			for i < len(src) && src[i] >= '0' && src[i] <= '9' {
				i++
			}
			if i < len(src) && src[i] == '.' {
				i++
				for i < len(src) && src[i] >= '0' && src[i] <= '9' {
					i++
				}
			}
			n, err := strconv.ParseFloat(src[start:i], 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q at offset %d", src[start:i], start)
			}
			toks = append(toks, scorerToken{off: start, kind: "num", text: src[start:i], num: n})
		case c == '\'' || c == '"':
			quote := c
			start := i
			i++
			for i < len(src) && src[i] != quote {
				if src[i] == '\n' || src[i] == '\r' {
					return nil, fmt.Errorf("string literal must not contain a newline (opened at offset %d)", start)
				}
				i++
			}
			if i >= len(src) {
				return nil, fmt.Errorf("unterminated string literal opened at offset %d — close it with %c", start, quote)
			}
			toks = append(toks, scorerToken{off: start, kind: "str", text: src[start+1 : i]})
			i++
		case isScorerIdentStart(c):
			start := i
			for i < len(src) && isScorerIdentByte(src[i]) {
				i++
			}
			toks = append(toks, scorerToken{off: start, kind: "ident", text: src[start:i]})
		default:
			// Two-char operators first.
			if i+1 < len(src) {
				pair := src[i : i+2]
				if pair == "||" || pair == "&&" || pair == "==" || pair == "!=" || pair == ">=" || pair == "<=" {
					toks = append(toks, scorerToken{off: i, kind: "op", text: pair})
					i += 2
					continue
				}
			}
			switch c {
			case '>', '<', '+', '-', '*', '/', '!', '(', ')', ',':
				toks = append(toks, scorerToken{off: i, kind: "op", text: string(c)})
				i++
			default:
				return nil, fmt.Errorf("unexpected character %q at offset %d — scorers allow numbers, quoted strings, the variables input/expected/output, comparisons (== != > < >= <=), booleans (! && ||), arithmetic (+ - * /), and the functions %s", string(c), i, strings.Join(scorerAllowlistNames, ", "))
			}
		}
		if len(toks) > scorerMaxTokens {
			return nil, fmt.Errorf("expression too large: more than %d tokens — keep scorers to a single formula", scorerMaxTokens)
		}
	}
	return toks, nil
}

func isScorerIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isScorerIdentByte(c byte) bool {
	return isScorerIdentStart(c) || (c >= '0' && c <= '9')
}

// scorerParser is a recursive-descent parser over the lexed token stream.
// Precedence, low to high: || , && , comparison , + - , * / , unary ! - .
type scorerParser struct {
	toks []scorerToken
	pos  int
}

func (p *scorerParser) peek() *scorerToken {
	if p.pos >= len(p.toks) {
		return nil
	}
	return &p.toks[p.pos]
}

func (p *scorerParser) eatOp(text string) bool {
	if t := p.peek(); t != nil && t.kind == "op" && t.text == text {
		p.pos++
		return true
	}
	return false
}

// parseExpr parses one full expression at the given nesting depth.
func (p *scorerParser) parseExpr(depth int) (scorerNode, error) {
	if depth > scorerMaxDepth {
		return nil, fmt.Errorf("expression too deeply nested (over %d levels) — flatten the formula", scorerMaxDepth)
	}
	return p.parseOr(depth)
}

func (p *scorerParser) parseOr(depth int) (scorerNode, error) {
	left, err := p.parseAnd(depth)
	if err != nil {
		return nil, err
	}
	for p.eatOp("||") {
		right, err := p.parseAnd(depth)
		if err != nil {
			return nil, err
		}
		requireNumericOperands("||", left, right)
		left = &scorerBinOpNode{op: "||", l: left, r: right, operandK: scorerNum}
	}
	return left, nil
}

func (p *scorerParser) parseAnd(depth int) (scorerNode, error) {
	left, err := p.parseCompare(depth)
	if err != nil {
		return nil, err
	}
	for p.eatOp("&&") {
		right, err := p.parseCompare(depth)
		if err != nil {
			return nil, err
		}
		requireNumericOperands("&&", left, right)
		left = &scorerBinOpNode{op: "&&", l: left, r: right, operandK: scorerNum}
	}
	return left, nil
}

// requireNumericOperands statically type-checks the boolean operators: they
// take numbers only, so `expected && output` is a refusal at upload, not a
// silent zero at scoring time.
func requireNumericOperands(op string, l, r scorerNode) {
	if l.kind() != scorerNum || r.kind() != scorerNum {
		panicScorerTypeErr(op, "the && and || operators take numbers only — comparisons like >= produce them")
	}
}

func (p *scorerParser) parseCompare(depth int) (scorerNode, error) {
	left, err := p.parseAdd(depth)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t != nil && t.kind == "op" {
		switch t.text {
		case "==", "!=", ">", "<", ">=", "<=":
			p.pos++
			right, err := p.parseAdd(depth)
			if err != nil {
				return nil, err
			}
			node := &scorerBinOpNode{op: t.text, l: left, r: right, operandK: operandKindForCompare(t.text, left, right)}
			return node, nil
		}
	}
	return left, nil
}

// operandKindForCompare statically type-checks one comparison: strings may
// only be compared for equality; ordering operators take numbers only.
func operandKindForCompare(op string, l, r scorerNode) scorerKind {
	lk, rk := l.kind(), r.kind()
	if lk != rk {
		reason := "cannot compare a number with a string"
		if lk == scorerStr || rk == scorerStr {
			reason = "cannot compare a string with a number"
		}
		panicScorerTypeErr(op, reason)
	}
	if lk == scorerStr && op != "==" && op != "!=" {
		panicScorerTypeErr(op, "strings can only be compared for equality (== !=); ordering operators take numbers")
	}
	return lk
}

// scorerTypeErr is a sentinel error the parser turns into a refusal; the
// type-check helpers raise it through a panic-in-parse so the recursive
// descent stays readable. It is recovered exactly once, in
// compileSkillScorer, and never escapes compilation.
type scorerTypeErr struct{ msg string }

func (e *scorerTypeErr) Error() string { return e.msg }

func panicScorerTypeErr(op, reason string) {
	panic(&scorerTypeErr{msg: fmt.Sprintf("type mismatch at %q: %s", op, reason)})
}

func (p *scorerParser) parseAdd(depth int) (scorerNode, error) {
	left, err := p.parseMul(depth)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != "op" || (t.text != "+" && t.text != "-") {
			return left, nil
		}
		op := t.text
		p.pos++
		right, err := p.parseMul(depth)
		if err != nil {
			return nil, err
		}
		left = &scorerBinOpNode{op: op, l: left, r: right, operandK: operandKindForArith(op, left, right)}
	}
}

func operandKindForArith(op string, l, r scorerNode) scorerKind {
	if l.kind() != scorerNum || r.kind() != scorerNum {
		panicScorerTypeErr(op, "arithmetic takes numbers only — wrap strings in len(), overlap(), or contains()")
	}
	return scorerNum
}

func (p *scorerParser) parseMul(depth int) (scorerNode, error) {
	left, err := p.parseUnary(depth)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != "op" || (t.text != "*" && t.text != "/") {
			return left, nil
		}
		op := t.text
		p.pos++
		right, err := p.parseUnary(depth)
		if err != nil {
			return nil, err
		}
		left = &scorerBinOpNode{op: op, l: left, r: right, operandK: operandKindForArith(op, left, right)}
	}
}

func (p *scorerParser) parseUnary(depth int) (scorerNode, error) {
	if t := p.peek(); t != nil && t.kind == "op" && (t.text == "!" || t.text == "-") {
		p.pos++
		x, err := p.parseUnary(depth + 1)
		if err != nil {
			return nil, err
		}
		if x.kind() != scorerNum {
			panicScorerTypeErr(t.text, "the ! and - operators take numbers only")
		}
		return &scorerUnaryNode{op: t.text, x: x, operand: scorerNum}, nil
	}
	return p.parsePrimary(depth)
}

func (p *scorerParser) parsePrimary(depth int) (scorerNode, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	switch t.kind {
	case "num":
		p.pos++
		return scorerNumNode{v: t.num}, nil
	case "str":
		p.pos++
		return scorerStrNode{v: t.text}, nil
	case "ident":
		p.pos++
		// Parenthesized call → allowlisted function; bare identifier → one of
		// the three bound variables.
		if p.eatOp("(") {
			fn, ok := scorerFn[t.text]
			if !ok {
				return nil, fmt.Errorf("unknown function %q at offset %d (allowed: %s)", t.text, t.off, strings.Join(scorerAllowlistNames, ", "))
			}
			var args []scorerNode
			if !p.eatOp(")") {
				for {
					a, err := p.parseExpr(depth + 1)
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.eatOp(",") {
						continue
					}
					break
				}
				if !p.eatOp(")") {
					return nil, fmt.Errorf("missing closing parenthesis for %s( at offset %d", t.text, t.off)
				}
			}
			if len(args) != len(fn.args) {
				return nil, fmt.Errorf("%s takes %d argument%s, got %d (offset %d)", t.text, len(fn.args), pluralSuffix(len(fn.args)), len(args), t.off)
			}
			for i, a := range args {
				if a.kind() != fn.args[i] {
					return nil, fmt.Errorf("%s argument %d is %s, need %s (offset %d)", t.text, i+1,
						scorerKindName(a.kind()), scorerKindName(fn.args[i]), t.off)
				}
			}
			return &scorerCallNode{name: t.text, args: args, argKinds: fn.args, ret: fn.ret, fn: fn.apply}, nil
		}
		switch t.text {
		case "input", "expected", "output":
			return scorerVarNode{name: t.text}, nil
		default:
			return nil, fmt.Errorf("unknown variable %q at offset %d (allowed: input, expected, output)", t.text, t.off)
		}
	default: // "op"
		if t.text == "(" {
			p.pos++
			x, err := p.parseExpr(depth + 1)
			if err != nil {
				return nil, err
			}
			if !p.eatOp(")") {
				return nil, fmt.Errorf("missing closing parenthesis at offset %d", t.off)
			}
			return x, nil
		}
		return nil, fmt.Errorf("unexpected token %q at offset %d", t.text, t.off)
	}
}

func scorerKindName(k scorerKind) string {
	if k == scorerStr {
		return "a string"
	}
	return "a number"
}

// skillScorer is one compiled scorer: its canonical source text plus the
// total, pure evaluation closure. Compiled once per distinct source per
// pipeline run (upload, gate, simulation) and reused for every case.
type skillScorer struct {
	Source string
	fn     scorerNode
}

// score computes the case's score in [0,1]: the expression value clamped.
// The output argument is the skill/candidate body being scored against the
// case — the same proxy the v0 heuristic used, now operator-defined.
func (sc *skillScorer) score(input, expected, output string) float64 {
	v := sc.fn.eval(input, expected, output)
	if v.kind != scorerNum {
		return 0 // statically unreachable: compile enforces a numeric root
	}
	if v.num < 0 {
		return 0
	}
	if v.num > 1 {
		return 1
	}
	return v.num
}

// compileSkillScorer compiles one scorer expression (a non-empty source that
// is not a built-in name). All bounds and type checks run here, so evaluation
// itself is total.
func compileSkillScorer(source string) (*skillScorer, error) {
	src := strings.TrimSpace(source)
	if src == "" {
		return nil, fmt.Errorf("scorer expression is empty")
	}
	if len(src) > scorerExprMaxBytes {
		return nil, fmt.Errorf("scorer expression too long: %d bytes exceeds the %d-byte cap — factor the formula into overlap()/contains() calls", len(src), scorerExprMaxBytes)
	}
	toks, err := lexScorer(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("scorer expression is empty")
	}
	p := &scorerParser{toks: toks}
	var root scorerNode
	func() {
		defer func() {
			if r := recover(); r != nil {
				if te, ok := r.(*scorerTypeErr); ok {
					err = te
					return
				}
				panic(r)
			}
		}()
		root, err = p.parseExpr(0)
	}()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t != nil {
		return nil, fmt.Errorf("unexpected token %q at offset %d — the expression must be one formula", t.text, t.off)
	}
	if root.kind() != scorerNum {
		return nil, fmt.Errorf("a scorer must evaluate to a number — comparisons like == and >= and the overlap/contains functions produce one")
	}
	return &skillScorer{Source: src, fn: root}, nil
}

// resolveSkillScorer maps a case's scorer field onto its compiled scorer:
// empty and "exact" mean the built-in exact match; anything else must be a
// sandboxed expression, and an unresolvable value is a refusal, never a
// silent fallback to the default.
func resolveSkillScorer(source string) (*skillScorer, error) {
	name := strings.TrimSpace(source)
	if name == "" || strings.EqualFold(name, defaultScorerName) {
		return &skillScorer{
			Source: defaultScorerName,
			fn:     exactScorerNode{},
		}, nil
	}
	return compileSkillScorer(name)
}

// exactScorerNode is the built-in exact-match scorer: 1 when the trimmed
// output equals the trimmed expected answer, else 0.
type exactScorerNode struct{}

func (exactScorerNode) eval(_, expected, output string) scorerVal {
	if strings.TrimSpace(output) == strings.TrimSpace(expected) {
		return scorerVal{kind: scorerNum, num: 1}
	}
	return scorerVal{kind: scorerNum, num: 0}
}
func (exactScorerNode) kind() scorerKind { return scorerNum }

// scorerSet compiles the distinct scorer sources used by one eval set (or one
// scoring pass) into a reusable registry. The set is the versioning unit: its
// identity is the sorted list of custom sources, hashed into the scorer
// version and mixed into the eval hash.
type scorerSet struct {
	scorers map[string]*skillScorer
}

// compileScorerSet resolves every distinct scorer source in cases. A failure
// names the source; the caller attributes it to rows.
func compileScorerSet(cases []EvalCase) (*scorerSet, error) {
	set := &scorerSet{scorers: map[string]*skillScorer{}}
	seen := map[string]bool{}
	for _, c := range cases {
		src := strings.TrimSpace(c.Scorer)
		if src == "" || seen[src] {
			continue
		}
		seen[src] = true
		sc, err := resolveSkillScorer(src)
		if err != nil {
			return nil, fmt.Errorf("scorer %q refused: %v", src, err)
		}
		set.scorers[src] = sc
	}
	return set, nil
}

// forCase returns the scorer for one case, always non-nil: empty and
// "exact" fall back to the built-in default (already registered).
func (ss *scorerSet) forCase(c EvalCase) *skillScorer {
	src := strings.TrimSpace(c.Scorer)
	if sc, ok := ss.scorers[src]; ok {
		return sc
	}
	// Unknown source (defensive: compileScorerSet registered every distinct
	// source). Resolve on demand; a failure here is impossible for sources
	// the set was compiled from.
	sc, err := resolveSkillScorer(src)
	if err != nil {
		return &skillScorer{Source: defaultScorerName, fn: exactScorerNode{}}
	}
	return sc
}

// customSources lists the distinct non-default scorer sources, sorted, for
// versioning and meta display.
func (ss *scorerSet) customSources() []string {
	var out []string
	for src := range ss.scorers {
		if src == "" || strings.EqualFold(src, defaultScorerName) {
			continue
		}
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}

// version is the scorer set's derived version: "v1" for default-only sets,
// "v1-<hash12>" once any custom scorer joins. Deterministic in the sorted
// sources, so an identical set always yields the identical version.
func (ss *scorerSet) version() string {
	return EvalScorerVersionFor(ss.customSources())
}

// EvalScorerVersionFor derives the scorer version for a list of custom scorer
// sources (order-insensitive). Empty means the eval set scored under the
// default scorer alone.
func EvalScorerVersionFor(customScorers []string) string {
	clean := make([]string, 0, len(customScorers))
	for _, s := range customScorers {
		if t := strings.TrimSpace(s); t != "" && !strings.EqualFold(t, defaultScorerName) {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		return EvalScorerVersionBase
	}
	sort.Strings(clean)
	h := sha256.New()
	for _, s := range clean {
		h.Write([]byte("scorer\x00" + s + "\n"))
	}
	return EvalScorerVersionBase + "-" + hex.EncodeToString(h.Sum(nil))[:scorerVersionHashLen]
}

// scoreEvalSplit scores one split of cases against a candidate/skill body
// (the scorer's `output`), returning the mean per-case score rounded to 4
// decimals — the exact arithmetic the audit's ValidationScore carries, so a
// recomputation from stored data compares bitwise-equal.
func (ss *scorerSet) scoreEvalSplit(cases []EvalCase, output string) float64 {
	if len(cases) == 0 {
		return 0
	}
	total := 0.0
	for _, c := range cases {
		total += ss.forCase(c).score(c.Input, c.Expected, output)
	}
	return roundScore4(total / float64(len(cases)))
}

// roundScore4 rounds to 4 decimals the way every stored score is rounded
// (RBestScore, evalBaselineS0 before it).
func roundScore4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
