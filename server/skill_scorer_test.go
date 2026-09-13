package server

import (
	"strings"
	"testing"
)

// Story 11 (B2) tests: the sandboxed scorer registry. The happy shapes
// (built-in exact, inline expressions), the refusal classes (unknown function,
// unknown variable, type mismatch, unterminated string, too long, non-numeric
// root, too deep), the totality guarantees (clamping, division by zero), and
// the derived versioning.

func TestScorerExactDefault(t *testing.T) {
	sc, err := resolveSkillScorer("")
	if err != nil {
		t.Fatalf("empty scorer must resolve to the default: %v", err)
	}
	if sc.Source != defaultScorerName {
		t.Errorf("default source = %q, want %q", sc.Source, defaultScorerName)
	}
	if got := sc.score("in", "hello world", "hello world"); got != 1 {
		t.Errorf("exact match = %v, want 1", got)
	}
	if got := sc.score("in", "hello world", "hello"); got != 0 {
		t.Errorf("non-match = %v, want 0", got)
	}
	// Whitespace-trimmed equality, case-sensitive exactness.
	if got := sc.score("in", "  hi  ", " hi "); got != 1 {
		t.Errorf("trimmed equality = %v, want 1", got)
	}
	if got := sc.score("in", "hi", "HI"); got != 0 {
		t.Errorf("case-folded = %v, want 0 (exact is case-sensitive)", got)
	}
	if _, err := resolveSkillScorer("exact"); err != nil {
		t.Fatalf("the built-in name must resolve: %v", err)
	}
}

func TestScorerOverlapFunction(t *testing.T) {
	sc, err := compileSkillScorer("overlap(expected, output)")
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	// overlap(expected, output): fraction of expected's tokens in output.
	if got := sc.score("i", "alpha beta gamma", "x alpha beta y"); got != 2.0/3.0 {
		t.Errorf("partial overlap = %v, want 0.6667", got)
	}
	if got := sc.score("i", "alpha beta", "alpha beta gamma"); got != 1 {
		t.Errorf("full coverage = %v, want 1", got)
	}
	if got := sc.score("i", "", "anything"); got != 0 {
		t.Errorf("no expected tokens = %v, want 0", got)
	}
}

func TestScorerFullFormula(t *testing.T) {
	sc, err := compileSkillScorer(`overlap(expected, output) >= 0.5 && contains(lower(output), "action")`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if got := sc.score("i", "alpha beta gamma delta", "alpha beta ACTION extra"); got != 1 {
		t.Errorf("passing formula = %v, want 1", got)
	}
	if got := sc.score("i", "alpha beta gamma delta", "alpha beta no marker"); got != 0 {
		t.Errorf("failing formula = %v, want 0", got)
	}
	// Arithmetic, variables, and a graded result: three quarters of the
	// expected tokens halved plus the length dividend.
	sc2, err := compileSkillScorer("overlap(expected, output) / 2 + len(output) / 40")
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if got := sc2.score("i", "alpha beta gamma delta", "alpha beta gamma"); got != 0.775 {
		t.Errorf("graded formula = %v, want 0.775 (0.75/2 + 16/40)", got)
	}
}

func TestScorerClampedAndTotal(t *testing.T) {
	// Values outside [0,1] clamp into it at scoring time.
	sc, err := compileSkillScorer("len(output) / 10")
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if got := sc.score("i", "x", strings.Repeat("z", 50)); got != 1 {
		t.Errorf("over-range score = %v, want clamped 1", got)
	}
	if got := sc.score("i", "x", "short"); got != 0.5 {
		t.Errorf("in-range score = %v, want 0.5", got)
	}
	// Division by zero is total (0), never an error or NaN.
	zero, err := compileSkillScorer("len(output) / (len(input) - len(input))")
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if got := zero.score("i", "x", "y"); got != 0 {
		t.Errorf("zero divisor = %v, want 0", got)
	}
}

func TestScorerRefusals(t *testing.T) {
	refusals := []struct {
		name, expr, want string
	}{
		{"unknown function", `panic(expected)`, "unknown function"},
		{"unknown variable", `overlap(secret, output)`, "unknown variable"},
		{"type mismatch arith", `expected + 1`, "type mismatch"},
		{"type mismatch compare", `expected > output`, "type mismatch"},
		{"type mismatch bool", `expected && output`, "type mismatch"},
		{"type mismatch or", `"a" || "b"`, "type mismatch"},
		{"string ordering", `"a" < "b"`, "type mismatch"},
		{"unterminated string", `contains(output, "unterminated)`, "unterminated string"},
		{"string newline", "contains(output, \"a\nb\")", "newline"},
		{"bad char", `expected; output`, "unexpected character"},
		{"arity", `overlap(expected)`, "takes 2 arguments"},
		{"arg type", `overlap(expected, 1)`, "need a string"},
		{"non-numeric root", `lower(output)`, "must evaluate to a number"},
		{"trailing token", `overlap(expected, output) 1`, "unexpected token"},
		{"unclosed paren", `overlap(expected, output`, "closing parenthesis"},
		{"too long", `contains(output, "` + strings.Repeat("a", 600) + `")`, "too long"},
		{"too deep", strings.Repeat("(", 30) + "1" + strings.Repeat(")", 30), "too deeply nested"},
		{"empty", "   ", "empty"},
	}
	for _, tc := range refusals {
		_, err := compileSkillScorer(tc.expr)
		if err == nil {
			t.Errorf("%s: expected refusal, got none", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refusal %q must name %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestScorerDeterministicAndPure(t *testing.T) {
	sc, err := compileSkillScorer(`overlap(expected, output) >= 0.5`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	first := sc.score("some input", "alpha beta gamma", "alpha beta body")
	for i := 0; i < 50; i++ {
		if got := sc.score("some input", "alpha beta gamma", "alpha beta body"); got != first {
			// Recompiling the same source must behave identically, and repeated
			// evaluations must not drift: the evaluator keeps no state.
			t.Fatalf("evaluation drifted: %v != %v", got, first)
		}
	}
	same, err := compileSkillScorer(`overlap(expected, output) >= 0.5`)
	if err != nil {
		t.Fatalf("recompile failed: %v", err)
	}
	if same.score("some input", "alpha beta gamma", "alpha beta body") != first {
		t.Error("the same source must compile to the same behavior")
	}
}

func TestEvalScorerVersionBumps(t *testing.T) {
	a := EvalScorerVersionFor([]string{"overlap(expected, output) >= 0.5"})
	b := EvalScorerVersionFor([]string{"overlap(expected, output) >= 0.5"})
	if a != b {
		t.Errorf("the same set must derive the same version: %q vs %q", a, b)
	}
	if a == EvalScorerVersionBase || a == SkillAuditScorerVersion {
		t.Errorf("a custom set must bump past %q, got %q", EvalScorerVersionBase, a)
	}
	// Order-insensitive: the version is over the sorted set.
	c := EvalScorerVersionFor([]string{"overlap(expected, output) >= 0.5", `contains(output, "x")`})
	d := EvalScorerVersionFor([]string{`contains(output, "x")`, "overlap(expected, output) >= 0.5"})
	if c != d {
		t.Errorf("version must be order-insensitive: %q vs %q", c, d)
	}
	if c == a {
		t.Error("a changed scorer set must bump the version")
	}
	if len(c) != len(EvalScorerVersionBase)+1+12 {
		t.Errorf("custom version %q must be %q + '-' + 12 hex", c, EvalScorerVersionBase)
	}
	// A changed expression bumps; the same text with different spacing is the
	// same source and must NOT bump.
	e := EvalScorerVersionFor([]string{"overlap(expected, output)>=0.5"})
	if e == a {
		t.Error("a changed expression must bump the version")
	}
}

func TestScorerSetScoresSplits(t *testing.T) {
	cases := []EvalCase{
		{Input: "i1", Expected: "alpha beta", Scorer: `overlap(expected, output) >= 0.5`},
		{Input: "i2", Expected: "gamma delta", Scorer: `overlap(expected, output) >= 0.5`},
		{Input: "i3", Expected: "plain", Scorer: ""},
	}
	set, err := compileScorerSet(cases)
	if err != nil {
		t.Fatalf("compileScorerSet failed: %v", err)
	}
	// Output covering alpha only: case 1 passes (1), cases 2-3 fail → 1/3.
	if got := set.scoreEvalSplit(cases, "alpha"); got != 0.3333 {
		t.Errorf("split score = %v, want 0.3333", got)
	}
	// The two overlap cases pass with the full vocabulary; the default exact
	// case still demands literal equality, so 2 of 3 = 0.6667 — the point
	// being that per-case scorers are independent.
	if got := set.scoreEvalSplit(cases, "alpha beta gamma delta"); got != 0.6667 {
		t.Errorf("full vocabulary = %v, want 0.6667", got)
	}
	if got := set.scoreEvalSplit(nil, "anything"); got != 0 {
		t.Errorf("empty split = %v, want 0", got)
	}
	if srcs := set.customSources(); len(srcs) != 1 || srcs[0] != `overlap(expected, output) >= 0.5` {
		t.Errorf("custom sources = %v", srcs)
	}
}
