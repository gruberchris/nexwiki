package server

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file holds story 05 of the wikiskill evolution support plan: validated
// training-data intake for evolution jobs (Phase C/C2 + E2).
//
// WHAT IT DOES: an operator (or a token-holding harness) uploads one eval file
// (CSV, JSONL, JSON, text, or logs — the asset-upload extension conventions)
// carrying task cases shaped {input, expected [, scorer]}. The format gate
// parses, validates, and either stores deterministic train/val splits or
// refuses with per-issue fix-it errors. On success it also records the S0
// baseline (the current skill's score on val under the story 03 v0 scorer),
// a SHA-256 eval hash, a 5-sample dry run, and a static cost-estimate stub
// (fields only, for story F3 budgets — no enforcement here).
//
// WHAT IT DOES NOT DO (later stories own these): scorer versioning (story 11
// plugs into SkillAuditScorerVersion without touching this gate), the wizard
// UI (story 08), and budget enforcement (F3).
//
// UNTRUSTED-DATA BOUNDARY (E2): uploaded cases are data, never instructions.
// They are stored as JSONL under the job's files directory, quoted (truncated)
// in errors and dry-run previews, and never interpolated into shell, argv, or
// env — the runner (skill_runner.go) only ever receives job JSON over stdin.
// Secret/PII scanning runs BEFORE any write: a hit stores nothing, and
// refusal messages carry class/offset/length only, never the matched text
// (the write-path refuse pattern from secrets.go, fail-closed: the eval path
// always refuses, even when NEXWIKI_SECRET_SCAN=warn, because bulk eval data
// is never annotated-and-kept).
//
// STORAGE: eval sets live alongside their job, mirroring the story 01/04
// flat-file conventions (data JSON, never indexed, never executed):
//
//	data/skill_jobs/<id>.files/eval/train.jsonl   — canonical train cases
//	data/skill_jobs/<id>.files/eval/val.jsonl     — canonical val cases
//	data/skill_jobs/<id>.files/eval/meta.json     — EvalMeta (hash, S0, dry run, estimate)
//
// The job record itself carries the summary fields (eval hash, split counts,
// S0, scorer version) so operators can read readiness off the job JSON.

// Eval intake configuration. ALL new env is NEXWIKI_-prefixed per the repo
// convention. Minimum split counts are operator-tunable; the defaults keep
// small experiments honest while the gate refuses toy sets.
const (
	// EvalMinTrainEnv overrides the minimum accepted train cases (default 30).
	EvalMinTrainEnv = "NEXWIKI_EVAL_MIN_TRAIN"
	// EvalMinValEnv overrides the minimum accepted val cases (default 10).
	EvalMinValEnv = "NEXWIKI_EVAL_MIN_VAL"
)

// DefaultEvalMinTrain and DefaultEvalMinVal are the gate floors when the env
// overrides above are unset.
const (
	DefaultEvalMinTrain = 30
	DefaultEvalMinVal   = 10
)

// Eval upload caps: a harness must not fill the data directory unchecked, and
// a single pathological case must not blow up the baseline pass.
const (
	evalMaxContentBytes = 2 << 20  // 2 MiB of uploaded text per call
	evalMaxCases        = 5000     // cases per eval set
	evalMaxFieldBytes   = 64 << 10 // bytes per input/expected/scorer field
	evalPreviewLen      = 120      // quoted preview length in results/errors
	evalLeakPreviewLen  = 80       // quoted preview length for leakage reports
)

// evalAllowedExts reuses the asset-upload allowlist conventions (handlers.go):
// data/text files only, never images or executables.
var evalAllowedExts = map[string]bool{
	".csv": true, ".json": true, ".jsonl": true, ".ndjson": true,
	".txt": true, ".log": true, ".md": true,
}

// EvalCase is one training case. Input and expected are required; scorer is an
// optional per-case scorer hint carried through to the eval files (story 11
// interprets it — this story only preserves it).
type EvalCase struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Scorer   string `json:"scorer,omitempty"`
}

// evalPIIPatterns extends the write-path secret scan for bulk eval data with
// the two PII shapes operators paste most often. Like secretPatterns they
// match high-signal shapes only, and matches are reported by class/offset/
// length — never echoed. The placeholder allowlist (isPlaceholder) applies to
// these too, so <your-email> and user@example.com stay writable.
var evalPIIPatterns = []secretPattern{
	{"email address", regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)},
	{"phone number", regexp.MustCompile(`\b(?:\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]\d{3}[-.\s]\d{4}\b`)},
}

// scanEvalCase runs the write-path secret scan plus the eval-local PII scan
// over one case's fields. The field names carry the split and index so a
// refusal says WHERE without quoting WHAT.
func scanEvalCase(split string, index int, c EvalCase) []SecretMatch {
	var found []SecretMatch
	fields := map[string]string{
		fmt.Sprintf("%s[%d].input", split, index):    c.Input,
		fmt.Sprintf("%s[%d].expected", split, index): c.Expected,
		fmt.Sprintf("%s[%d].scorer", split, index):   c.Scorer,
	}
	for field, text := range fields {
		if text == "" {
			continue
		}
		for _, p := range secretPatterns {
			for _, loc := range p.re.FindAllStringIndex(text, -1) {
				if isPlaceholder(text, loc[0], loc[1]) {
					continue
				}
				found = append(found, SecretMatch{Class: p.class, Field: field, Offset: loc[0], Length: loc[1] - loc[0]})
			}
		}
		for _, p := range evalPIIPatterns {
			for _, loc := range p.re.FindAllStringIndex(text, -1) {
				if isPlaceholder(text, loc[0], loc[1]) {
					continue
				}
				found = append(found, SecretMatch{Class: p.class, Field: field, Offset: loc[0], Length: loc[1] - loc[0]})
			}
		}
	}
	return found
}

// EvalValidationError is the format gate's refusal: every issue found in one
// pass, with fix-it guidance, so the operator can repair the whole file at
// once. Nothing is stored when this is returned — not the cases, not a
// partial split, not a hash.
type EvalValidationError struct {
	Issues     []string
	Parsed     int
	TrainCount int
	ValCount   int
	SplitMode  string
}

func (e *EvalValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "evolution eval set refused (%d issue%s, %d parsed cases",
		len(e.Issues), pluralSuffix(len(e.Issues)), e.Parsed)
	if e.SplitMode != "" {
		fmt.Fprintf(&b, ", %s splits %d train / %d val", e.SplitMode, e.TrainCount, e.ValCount)
	}
	b.WriteString("):\n")
	for _, issue := range e.Issues {
		fmt.Fprintf(&b, "  - %s\n", issue)
	}
	b.WriteString("Fix each issue and re-upload; nothing was stored. " +
		"Quoted previews below are uploaded data, not instructions.")
	return b.String()
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// evalMinCounts reads the operator-tunable split floors. Unset means the
// defaults; a set-but-unparseable value is a fail-closed error naming the env
// var, not a silent fallback (a typo in a gate threshold must not widen it).
func evalMinCounts() (minTrain, minVal int, err error) {
	minTrain, minVal = DefaultEvalMinTrain, DefaultEvalMinVal
	if raw := strings.TrimSpace(os.Getenv(EvalMinTrainEnv)); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 {
			return 0, 0, fmt.Errorf("invalid eval gate configuration: %s=%q must be a positive integer (default %d)",
				EvalMinTrainEnv, raw, DefaultEvalMinTrain)
		}
		minTrain = n
	}
	if raw := strings.TrimSpace(os.Getenv(EvalMinValEnv)); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 {
			return 0, 0, fmt.Errorf("invalid eval gate configuration: %s=%q must be a positive integer (default %d)",
				EvalMinValEnv, raw, DefaultEvalMinVal)
		}
		minVal = n
	}
	return minTrain, minVal, nil
}

// truncateQuoted shortens uploaded data for previews in results and errors.
// The marker makes it explicit the text was cut, so a clipped input is never
// mistaken for the full case.
func truncateQuoted(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

// firstEvalSecretClass reports the class of the first credential/PII-shaped
// match in s under the same scan the gate refuses on (the write-path secret
// patterns plus the eval-local PII shapes, placeholder allowlist applied).
// The empty string means the text scanned clean.
func firstEvalSecretClass(s string) string {
	for _, p := range secretPatterns {
		for _, loc := range p.re.FindAllStringIndex(s, -1) {
			if !isPlaceholder(s, loc[0], loc[1]) {
				return p.class
			}
		}
	}
	for _, p := range evalPIIPatterns {
		for _, loc := range p.re.FindAllStringIndex(s, -1) {
			if !isPlaceholder(s, loc[0], loc[1]) {
				return p.class
			}
		}
	}
	return ""
}

// evalQuotedPreview renders an uploaded input for a dupe/leakage refusal line.
// The full (untruncated) text is scanned first: any secret hit replaces the
// whole preview with a class-named placeholder, so neither the value itself
// nor a truncation fragment of it can echo through the error text.
func evalQuotedPreview(input string, n int) string {
	if class := firstEvalSecretClass(input); class != "" {
		return fmt.Sprintf("<redacted: %s>", class)
	}
	return truncateQuoted(input, n)
}

// rawEvalCase is one parsed row plus its origin, before split assignment.
type rawEvalCase struct {
	Case  EvalCase
	Split string // "train", "val", or "" (auto)
	Row   int    // 1-based row/line number in the upload, for fix-it errors
}

// parsedEvalSet is the gate's working set: validated cases with their split
// assignment and provenance.
type parsedEvalSet struct {
	train []EvalCase
	val   []EvalCase
	mode  string // "auto" or "user"
	total int
}

// parseEvalContent routes one upload to its format parser by filename
// extension (the asset-upload convention: extension agrees with content).
func parseEvalContent(filename, content string) ([]rawEvalCase, error) {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	if !evalAllowedExts[ext] {
		return nil, &EvalValidationError{Issues: []string{
			fmt.Sprintf("unsupported eval file type %q: accepted extensions are .csv, .json, .jsonl, .ndjson, .txt, .log, .md (data/text files only, per the asset-upload allowlist)", ext),
		}}
	}
	if int64(len(content)) > evalMaxContentBytes {
		return nil, &EvalValidationError{Issues: []string{
			fmt.Sprintf("eval file too large: %d bytes exceeds the %d-byte per-upload cap — split the set across jobs or trim it", len(content), evalMaxContentBytes),
		}}
	}
	switch ext {
	case ".csv":
		return parseEvalCSV(content)
	case ".json":
		return parseEvalJSON(content)
	case ".jsonl", ".ndjson":
		return parseEvalJSONL(content)
	default: // .txt, .log, .md
		return parseEvalText(content)
	}
}

// foldKeys lowercases an object's keys so "Input" and "input" agree.
func foldKeys(obj map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(obj))
	for k, v := range obj {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// buildRawCase validates one case-shaped object into a rawEvalCase. It returns
// a fix-it issue instead of the case when the shape is wrong.
func buildRawCase(obj map[string]interface{}, row int) (rawEvalCase, string) {
	obj = foldKeys(obj)
	strField := func(name string) (string, bool) {
		v, ok := obj[name]
		if !ok || v == nil {
			return "", false
		}
		s, ok := v.(string)
		return s, ok
	}
	input, inputIsStr := strField("input")
	expected, expectedIsStr := strField("expected")
	if _, hasInput := obj["input"]; !hasInput {
		return rawEvalCase{}, fmt.Sprintf("row %d: missing required field \"input\" (each case needs {input, expected [, scorer]})", row)
	}
	if _, hasExpected := obj["expected"]; !hasExpected {
		return rawEvalCase{}, fmt.Sprintf("row %d: missing required field \"expected\" (each case needs {input, expected [, scorer]})", row)
	}
	if !inputIsStr {
		return rawEvalCase{}, fmt.Sprintf("row %d: field \"input\" must be a string", row)
	}
	if !expectedIsStr {
		return rawEvalCase{}, fmt.Sprintf("row %d: field \"expected\" must be a string", row)
	}
	scorer := ""
	if v, ok := obj["scorer"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return rawEvalCase{}, fmt.Sprintf("row %d: field \"scorer\" must be a string when present", row)
		}
		scorer = s
	}
	split := ""
	if v, ok := obj["split"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return rawEvalCase{}, fmt.Sprintf("row %d: field \"split\" must be \"train\" or \"val\" when present", row)
		}
		split = strings.ToLower(strings.TrimSpace(s))
		if split != "train" && split != "val" {
			return rawEvalCase{}, fmt.Sprintf("row %d: field \"split\" must be \"train\" or \"val\", got %q", row, truncateQuoted(s, 20))
		}
	}
	c := EvalCase{Input: strings.TrimSpace(input), Expected: strings.TrimSpace(expected), Scorer: strings.TrimSpace(scorer)}
	if c.Input == "" {
		return rawEvalCase{}, fmt.Sprintf("row %d: field \"input\" must not be empty", row)
	}
	if c.Expected == "" {
		return rawEvalCase{}, fmt.Sprintf("row %d: field \"expected\" must not be empty", row)
	}
	if len(c.Input) > evalMaxFieldBytes || len(c.Expected) > evalMaxFieldBytes || len(c.Scorer) > evalMaxFieldBytes {
		return rawEvalCase{}, fmt.Sprintf("row %d: a field exceeds the %d-byte per-field cap — shorten the case", row, evalMaxFieldBytes)
	}
	return rawEvalCase{Case: c, Split: split, Row: row}, ""
}

// parseEvalJSON accepts an array of cases, {"cases": [...]}, or user-supplied
// splits {"train": [...], "val": [...]}.
func parseEvalJSON(content string) ([]rawEvalCase, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, &EvalValidationError{Issues: []string{"empty eval file: no cases to parse"}}
	}
	var top interface{}
	if err := json.Unmarshal([]byte(content), &top); err != nil {
		return nil, &EvalValidationError{Issues: []string{fmt.Sprintf("invalid JSON: %v — the file must be an array of {input, expected} cases, {\"cases\": [...]}, or {\"train\": [...], \"val\": [...]}", err)}}
	}
	switch v := top.(type) {
	case []interface{}:
		return rawCasesFromList(v, 1)
	case map[string]interface{}:
		folded := foldKeys(v)
		if _, hasTrain := folded["train"]; hasTrain {
			return rawCasesFromSplits(folded)
		}
		if _, hasVal := folded["val"]; hasVal {
			return rawCasesFromSplits(folded)
		}
		casesRaw, ok := folded["cases"]
		if !ok {
			return nil, &EvalValidationError{Issues: []string{
				"JSON object must be {\"cases\": [...]} for auto-split or {\"train\": [...], \"val\": [...]} for user-supplied splits",
			}}
		}
		list, ok := casesRaw.([]interface{})
		if !ok {
			return nil, &EvalValidationError{Issues: []string{"field \"cases\" must be an array of {input, expected} objects"}}
		}
		return rawCasesFromList(list, 1)
	default:
		return nil, &EvalValidationError{Issues: []string{"JSON eval must be an array of cases, {\"cases\": [...]}, or {\"train\": [...], \"val\": [...]}"}}
	}
}

// rawCasesFromList validates a pool of case objects (auto-split downstream).
func rawCasesFromList(list []interface{}, baseRow int) ([]rawEvalCase, error) {
	if len(list) == 0 {
		return nil, &EvalValidationError{Issues: []string{"empty eval file: no cases to parse"}}
	}
	if len(list) > evalMaxCases {
		return nil, &EvalValidationError{Issues: []string{
			fmt.Sprintf("too many cases: %d exceeds the %d-case cap — split the set across jobs", len(list), evalMaxCases),
		}}
	}
	var out []rawEvalCase
	var issues []string
	for i, item := range list {
		obj, ok := item.(map[string]interface{})
		if !ok {
			issues = append(issues, fmt.Sprintf("row %d: each case must be an object shaped {input, expected [, scorer]}", baseRow+i))
			continue
		}
		rc, issue := buildRawCase(obj, baseRow+i)
		if issue != "" {
			issues = append(issues, issue)
			continue
		}
		out = append(out, rc)
	}
	if len(issues) > 0 {
		return nil, &EvalValidationError{Issues: issues, Parsed: len(list)}
	}
	return out, nil
}

// rawCasesFromSplits validates user-supplied train/val splits from a JSON
// envelope. Rows are numbered continuously (train first) for fix-it errors.
func rawCasesFromSplits(folded map[string]interface{}) ([]rawEvalCase, error) {
	trainRaw, trainOK := folded["train"].([]interface{})
	valRaw, valOK := folded["val"].([]interface{})
	if !trainOK || !valOK {
		return nil, &EvalValidationError{Issues: []string{
			"user-supplied splits need both \"train\" and \"val\" arrays of {input, expected} objects",
		}}
	}
	if len(trainRaw)+len(valRaw) > evalMaxCases {
		return nil, &EvalValidationError{Issues: []string{
			fmt.Sprintf("too many cases: %d exceeds the %d-case cap — split the set across jobs", len(trainRaw)+len(valRaw), evalMaxCases),
		}}
	}
	var out []rawEvalCase
	var issues []string
	row := 1
	for _, item := range trainRaw {
		obj, ok := item.(map[string]interface{})
		if !ok {
			issues = append(issues, fmt.Sprintf("train row %d: each case must be an object shaped {input, expected [, scorer]}", row))
			row++
			continue
		}
		rc, issue := buildRawCase(obj, row)
		if issue != "" {
			issues = append(issues, "train "+issue)
			row++
			continue
		}
		rc.Split = "train"
		out = append(out, rc)
		row++
	}
	for _, item := range valRaw {
		obj, ok := item.(map[string]interface{})
		if !ok {
			issues = append(issues, fmt.Sprintf("val row %d: each case must be an object shaped {input, expected [, scorer]}", row))
			row++
			continue
		}
		rc, issue := buildRawCase(obj, row)
		if issue != "" {
			issues = append(issues, "val "+issue)
			row++
			continue
		}
		rc.Split = "val"
		out = append(out, rc)
		row++
	}
	if len(issues) > 0 {
		return nil, &EvalValidationError{Issues: issues, Parsed: len(trainRaw) + len(valRaw), SplitMode: "user"}
	}
	return out, nil
}

// parseEvalJSONL accepts one case object per line. A per-record "split" field
// supplies user splits — all rows or none (mixed files are refused).
func parseEvalJSONL(content string) ([]rawEvalCase, error) {
	var out []rawEvalCase
	var issues []string
	lineNo := 0
	nonEmpty := 0
	for _, line := range strings.Split(content, "\n") {
		lineNo++
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmpty++
		if nonEmpty > evalMaxCases {
			return nil, &EvalValidationError{Issues: []string{
				fmt.Sprintf("too many cases: more than the %d-case cap — split the set across jobs", evalMaxCases),
			}}
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			issues = append(issues, fmt.Sprintf("line %d: invalid JSON object: %v", lineNo, err))
			continue
		}
		rc, issue := buildRawCase(obj, lineNo)
		if issue != "" {
			issues = append(issues, strings.Replace(issue, "row", "line", 1))
			continue
		}
		out = append(out, rc)
	}
	if nonEmpty == 0 {
		return nil, &EvalValidationError{Issues: []string{"empty eval file: no cases to parse"}}
	}
	if len(issues) > 0 {
		return nil, &EvalValidationError{Issues: issues, Parsed: nonEmpty}
	}
	return out, nil
}

// parseEvalCSV requires a header row with input and expected columns, plus
// optional scorer and split columns (all matched case-insensitively).
func parseEvalCSV(content string) ([]rawEvalCase, error) {
	r := csv.NewReader(strings.NewReader(content))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, &EvalValidationError{Issues: []string{fmt.Sprintf("invalid CSV: %v", err)}}
	}
	// Drop leading blank lines so the header is the first real row.
	for len(records) > 0 && isBlankRecord(records[0]) {
		records = records[1:]
	}
	if len(records) == 0 {
		return nil, &EvalValidationError{Issues: []string{"empty eval file: no cases to parse"}}
	}
	header := records[0]
	colIdx := map[string]int{}
	for i, h := range header {
		name := strings.ToLower(strings.TrimSpace(h))
		if _, dup := colIdx[name]; dup {
			continue
		}
		colIdx[name] = i
	}
	inIdx, hasInput := colIdx["input"]
	expIdx, hasExpected := colIdx["expected"]
	if !hasInput || !hasExpected {
		return nil, &EvalValidationError{Issues: []string{
			"CSV header must contain \"input\" and \"expected\" columns (optional: \"scorer\", \"split\" with train/val values)",
		}}
	}
	scorerIdx, hasScorer := colIdx["scorer"]
	splitIdx, hasSplit := colIdx["split"]
	var out []rawEvalCase
	var issues []string
	for i, rec := range records[1:] {
		row := i + 2 // 1-based including the header
		if isBlankRecord(rec) {
			continue
		}
		if len(out)+len(issues) >= evalMaxCases && evalMaxCases > 0 && len(out) >= evalMaxCases {
			return nil, &EvalValidationError{Issues: []string{
				fmt.Sprintf("too many cases: more than the %d-case cap — split the set across jobs", evalMaxCases),
			}}
		}
		cell := func(idx int) string {
			if idx < len(rec) {
				return rec[idx]
			}
			return ""
		}
		obj := map[string]interface{}{"input": cell(inIdx), "expected": cell(expIdx)}
		if hasScorer {
			obj["scorer"] = cell(scorerIdx)
		}
		if hasSplit {
			obj["split"] = cell(splitIdx)
		}
		rc, issue := buildRawCase(obj, row)
		if issue != "" {
			issues = append(issues, issue)
			continue
		}
		out = append(out, rc)
	}
	if len(out) == 0 && len(issues) == 0 {
		return nil, &EvalValidationError{Issues: []string{"empty eval file: a header with no case rows"}}
	}
	if len(issues) > 0 {
		return nil, &EvalValidationError{Issues: issues, Parsed: len(records) - 1}
	}
	return out, nil
}

func isBlankRecord(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// parseEvalText reads plain text/log/markdown: one case per non-empty line as
// "input ||| expected" (or a single TAB separator). Lines without a separator
// cannot carry the required schema and are reported per line.
func parseEvalText(content string) ([]rawEvalCase, error) {
	var out []rawEvalCase
	var issues []string
	lineNo := 0
	for _, line := range strings.Split(content, "\n") {
		lineNo++
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(out)+len(issues) >= evalMaxCases {
			return nil, &EvalValidationError{Issues: []string{
				fmt.Sprintf("too many cases: more than the %d-case cap — split the set across jobs", evalMaxCases),
			}}
		}
		input, expected, ok := splitTextCase(line)
		if !ok {
			issues = append(issues, fmt.Sprintf("line %d: text cases must be \"input ||| expected\" (or input<TAB>expected) — no separator found", lineNo))
			continue
		}
		rc, issue := buildRawCase(map[string]interface{}{"input": input, "expected": expected}, lineNo)
		if issue != "" {
			issues = append(issues, strings.Replace(issue, "row", "line", 1))
			continue
		}
		out = append(out, rc)
	}
	if len(out) == 0 && len(issues) == 0 {
		return nil, &EvalValidationError{Issues: []string{"empty eval file: no cases to parse"}}
	}
	if len(issues) > 0 {
		return nil, &EvalValidationError{Issues: issues, Parsed: len(out) + len(issues)}
	}
	return out, nil
}

func splitTextCase(line string) (input, expected string, ok bool) {
	if parts := strings.SplitN(line, "|||", 2); len(parts) == 2 {
		return parts[0], parts[1], true
	}
	if parts := strings.SplitN(line, "\t", 2); len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

// gateEvalSet runs the full format gate over parsed rows: mixed-split check,
// secret/PII scan (fail-closed, before anything is stored), dedupe, split
// assignment (user splits or deterministic auto-split), minimum counts, and
// train/val leakage. Every failure is collected into one EvalValidationError;
// a nil error means the set is safe to store.
func gateEvalSet(rows []rawEvalCase, minTrain, minVal int) (*parsedEvalSet, error) {
	total := len(rows)
	splitRows := 0
	for _, r := range rows {
		if r.Split != "" {
			splitRows++
		}
	}
	mode := "auto"
	if splitRows > 0 {
		if splitRows != total {
			return nil, &EvalValidationError{
				Issues: []string{
					fmt.Sprintf("mixed split assignment: %d of %d rows name a split — supply splits for every row (\"split\": train/val, or a CSV split column, or a JSON {\"train\":...,\"val\":...} envelope) or for none (auto-split)", splitRows, total),
				},
				Parsed: total,
			}
		}
		mode = "user"
	}

	// Secrets/PII first: bulk untrusted data is refused, never stored — even
	// in NEXWIKI_SECRET_SCAN=warn mode. Matches carry class/field/offset only.
	var issues []string
	for _, r := range rows {
		split := r.Split
		if split == "" {
			split = "pool"
		}
		for _, m := range scanEvalCase(split, r.Row, r.Case) {
			issues = append(issues, fmt.Sprintf("row %d: refused — apparent %s (see the write-path secret-scan rule: remove the value, use a <placeholder>, or keep it out of training data)", r.Row, m))
		}
	}

	// Dedupe: identical cases (trimmed input+expected+scorer) add no signal
	// and usually mean a concatenated file. Refused with row numbers.
	seen := map[string][]int{}
	for _, r := range rows {
		key := r.Case.Input + "\x00" + r.Case.Expected + "\x00" + r.Case.Scorer
		seen[key] = append(seen[key], r.Row)
	}
	dupGroups := 0
	for key, locs := range seen {
		if len(locs) < 2 {
			continue
		}
		dupGroups++
		if dupGroups > 10 {
			continue
		}
		rowList := make([]string, len(locs))
		for i, n := range locs {
			rowList[i] = strconv.Itoa(n)
		}
		preview := evalQuotedPreview(strings.SplitN(key, "\x00", 2)[0], evalLeakPreviewLen)
		issues = append(issues, fmt.Sprintf("duplicate cases: rows %s are identical (input %q) — dedupe the file and re-upload", strings.Join(rowList, ", "), preview))
	}
	if dupGroups > 10 {
		issues = append(issues, fmt.Sprintf("...and %d more duplicate groups", dupGroups-10))
	}

	// Split assignment.
	var train, val []EvalCase
	if mode == "user" {
		for _, r := range rows {
			if r.Split == "train" {
				train = append(train, r.Case)
			} else {
				val = append(val, r.Case)
			}
		}
	} else {
		// Deterministic order-preserving auto-split: the last quarter is val.
		// No shuffling, no RNG — the same file always yields the same splits.
		valN := total / 4
		if valN < 1 && total > 1 {
			valN = 1
		}
		trainN := total - valN
		for _, r := range rows[:trainN] {
			train = append(train, r.Case)
		}
		for _, r := range rows[trainN:] {
			val = append(val, r.Case)
		}
	}

	// Minimum counts, with the env names so the operator knows the knobs.
	if len(train) < minTrain || len(val) < minVal {
		issues = append(issues, fmt.Sprintf("eval set too small: %s splits hold %d train / %d val, need at least %d train / %d val (%s/%s) — add cases or lower the thresholds",
			mode, len(train), len(val), minTrain, minVal, EvalMinTrainEnv, EvalMinValEnv))
	}

	// Leakage: identical inputs across splits would let a tuned skill recite
	// val. Refused with the overlapping inputs (quoted, truncated) and counts.
	// A secret-bearing input is quoted as a redaction placeholder instead —
	// the leak must not be fixed by printing the secret into the transcript.
	trainRowsByInput := map[string][]int{}
	for _, c := range train {
		trainRowsByInput[c.Input] = append(trainRowsByInput[c.Input], 1)
	}
	valInputs := map[string][]int{}
	for i, c := range val {
		valInputs[c.Input] = append(valInputs[c.Input], i)
	}
	leaks := 0
	for input, vIdx := range valInputs {
		tIdx, ok := trainRowsByInput[input]
		if !ok {
			continue
		}
		leaks++
		if leaks > 10 {
			continue
		}
		issues = append(issues, fmt.Sprintf("train/val overlap (leakage): input %q appears in %d train and %d val cases — identical inputs across splits are refused; rewrite one side or move the cases into a single split",
			evalQuotedPreview(input, evalLeakPreviewLen), len(tIdx), len(vIdx)))
	}
	if leaks > 10 {
		issues = append(issues, fmt.Sprintf("...and %d more leaked inputs", leaks-10))
	}

	if len(issues) > 0 {
		return nil, &EvalValidationError{
			Issues: issues, Parsed: total,
			TrainCount: len(train), ValCount: len(val), SplitMode: mode,
		}
	}
	return &parsedEvalSet{train: train, val: val, mode: mode, total: total}, nil
}

// evalSetHash is the canonical content hash over the stored splits (the
// story-03 content-hash convention: SHA-256 hex). Split-tagged and
// order-sensitive: reordering or moving a case across splits changes it.
func evalSetHash(train, val []EvalCase) string {
	h := sha256.New()
	for _, c := range train {
		h.Write([]byte("train\x00" + c.Input + "\x00" + c.Expected + "\x00" + c.Scorer + "\n"))
	}
	for _, c := range val {
		h.Write([]byte("val\x00" + c.Input + "\x00" + c.Expected + "\x00" + c.Scorer + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// evalWordTokens folds text to a lowercase alphanumeric token set for the v0
// baseline heuristic.
func evalWordTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if tok != "" {
			out[tok] = true
		}
	}
	return out
}

// evalCasePasses is the v0 baseline rule: a val case "passes" when at least
// half of its expected-answer tokens already appear in the current skill body.
// Crude by design — it measures whether the skill as written covers the
// answer vocabulary, deterministically, with no model calls. Story 11
// replaces this with versioned scorers; the S0 shape (score in [0,1],
// stamped SkillAuditScorerVersion) stays.
func evalCasePasses(skillTokens map[string]bool, expected string) bool {
	exp := evalWordTokens(expected)
	if len(exp) == 0 {
		return false
	}
	hit := 0
	for tok := range exp {
		if skillTokens[tok] {
			hit++
		}
	}
	return float64(hit)/float64(len(exp)) >= 0.5
}

// evalBaselineS0 scores the current skill body against every val case and
// returns the mean pass rate in [0,1], rounded to 4 decimals like RBestScore.
func evalBaselineS0(skillBody string, val []EvalCase) float64 {
	if len(val) == 0 {
		return 0
	}
	skillTokens := evalWordTokens(skillBody)
	passes := 0
	for _, c := range val {
		if evalCasePasses(skillTokens, c.Expected) {
			passes++
		}
	}
	return math.Round(float64(passes)/float64(len(val))*10000) / 10000
}

// EvalDryRunSample is one of the 5-sample dry-run rows: quoted previews plus
// the v0 pass/fail, so the operator can sanity-check the baseline by eye.
type EvalDryRunSample struct {
	Index           int    `json:"index"`
	InputPreview    string `json:"input_preview"`
	ExpectedPreview string `json:"expected_preview"`
	Pass            bool   `json:"pass"`
}

// EvalCostEstimate is the static cost-estimate stub for story F3 budgets:
// recorded fields only, never enforced. Enforced is always false in story 05.
type EvalCostEstimate struct {
	EstimatedValCases int     `json:"estimated_val_cases"`
	EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
	BudgetEnforced    bool    `json:"budget_enforced"`
	Note              string  `json:"note"`
}

// EvalMeta is the stored record for one accepted eval upload.
type EvalMeta struct {
	JobID         string             `json:"job_id"`
	SkillSlug     string             `json:"skill_slug"`
	Filename      string             `json:"filename"`
	SplitMode     string             `json:"split_mode"`
	TrainCount    int                `json:"train_count"`
	ValCount      int                `json:"val_count"`
	EvalHash      string             `json:"eval_hash"`
	BaselineS0    float64            `json:"baseline_s0"`
	ScorerVersion string             `json:"scorer_version"`
	DryRun        []EvalDryRunSample `json:"dry_run"`
	Estimate      EvalCostEstimate   `json:"estimate"`
	UploadedAt    time.Time          `json:"uploaded_at"`
}

// EvalUploadResult is the success value: the stored meta plus file locations.
type EvalUploadResult struct {
	Meta      EvalMeta `json:"meta"`
	TrainPath string   `json:"train_path"`
	ValPath   string   `json:"val_path"`
	MetaPath  string   `json:"meta_path"`
}

// evalFilesDir resolves the per-job eval directory, confining it under the
// job's files directory the way jobFilesDir confines the job itself.
func (s *Storage) evalFilesDir(jobID string) (string, error) {
	base, err := s.jobFilesDir(jobID)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "eval"), nil
}

// writeEvalJSONL persists canonical cases, one JSON object per line.
func writeEvalJSONL(path string, cases []EvalCase) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to write eval split: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, c := range cases {
		if err := enc.Encode(c); err != nil {
			_ = f.Close()
			return fmt.Errorf("failed to encode eval case: %w", err)
		}
	}
	return f.Close()
}

// UploadEvolutionEvalSet validates one uploaded eval file through the format
// gate and, on success, stores deterministic train/val splits plus the S0
// baseline, eval hash, dry run, and cost-estimate stub alongside the job.
// Token-scoped to the job like every other lifecycle call; refused on
// terminal jobs (history is not rewritten). Any gate failure stores nothing.
func (s *Storage) UploadEvolutionEvalSet(jobID, token, filename, content string) (*EvalUploadResult, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || strings.ContainsAny(filename, `/\`) || filepath.Base(filename) != filename {
		return nil, fmt.Errorf("eval filename is required and must be a bare filename")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(jobID)
	if err != nil {
		return nil, err
	}
	if !verifyJobToken(job, token) {
		return nil, &JobAuthError{JobID: job.ID, Action: "upload eval set"}
	}
	if evolutionJobTerminal(job.Status) {
		return nil, fmt.Errorf("cannot upload eval set to evolution job '%s': already terminal (%s)", job.ID, job.Status)
	}

	rows, err := parseEvalContent(filename, content)
	if err != nil {
		return nil, err
	}
	minTrain, minVal, err := evalMinCounts()
	if err != nil {
		return nil, err
	}
	set, err := gateEvalSet(rows, minTrain, minVal)
	if err != nil {
		return nil, err
	}

	skill, err := s.GetArticle(job.SkillSlug)
	if err != nil {
		return nil, fmt.Errorf("cannot score eval baseline: skill not found: %s", job.SkillSlug)
	}
	if skill.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot score eval baseline: '%s' is no longer a Custom AI Skill", job.SkillSlug)
	}

	hash := evalSetHash(set.train, set.val)
	s0 := evalBaselineS0(skill.Content, set.val)
	skillTokens := evalWordTokens(skill.Content)
	dryN := len(set.val)
	if dryN > 5 {
		dryN = 5
	}
	dry := make([]EvalDryRunSample, 0, dryN)
	for i := 0; i < dryN; i++ {
		dry = append(dry, EvalDryRunSample{
			Index:           i,
			InputPreview:    truncateQuoted(set.val[i].Input, evalPreviewLen),
			ExpectedPreview: truncateQuoted(set.val[i].Expected, evalPreviewLen),
			Pass:            evalCasePasses(skillTokens, set.val[i].Expected),
		})
	}

	now := time.Now().UTC()
	meta := EvalMeta{
		JobID: job.ID, SkillSlug: job.SkillSlug, Filename: filepath.Base(filename),
		SplitMode: set.mode, TrainCount: len(set.train), ValCount: len(set.val),
		EvalHash: hash, BaselineS0: s0, ScorerVersion: SkillAuditScorerVersion,
		DryRun: dry,
		Estimate: EvalCostEstimate{
			EstimatedValCases: len(set.val), EstimatedCostUSD: 0, BudgetEnforced: false,
			Note: "static stub for F3 budgets; recorded only, never enforced",
		},
		UploadedAt: now,
	}

	dir, err := s.evalFilesDir(job.ID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create job eval directory: %w", err)
	}
	trainPath := filepath.Join(dir, "train.jsonl")
	valPath := filepath.Join(dir, "val.jsonl")
	metaPath := filepath.Join(dir, "meta.json")
	// Re-uploads replace the splits wholesale: the job record's hash always
	// describes exactly what is on disk.
	if err := writeEvalJSONL(trainPath, set.train); err != nil {
		return nil, err
	}
	if err := writeEvalJSONL(valPath, set.val); err != nil {
		return nil, err
	}
	rawMeta, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode eval meta: %w", err)
	}
	if err := os.WriteFile(metaPath, rawMeta, 0644); err != nil {
		return nil, fmt.Errorf("failed to write eval meta: %w", err)
	}

	job.EvalHash = hash
	job.EvalTrainCount = len(set.train)
	job.EvalValCount = len(set.val)
	job.BaselineS0 = s0
	job.BaselineScorer = SkillAuditScorerVersion
	job.EvalUploadedAt = now
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, err
	}
	logRunnerf("eval set uploaded for job %s: %d train / %d val (%s), hash %.12s, S0 %.4f (%s)",
		job.ID, len(set.train), len(set.val), set.mode, hash, s0, SkillAuditScorerVersion)
	return &EvalUploadResult{Meta: meta, TrainPath: trainPath, ValPath: valPath, MetaPath: metaPath}, nil
}

// ReadEvolutionEvalSet loads the stored splits and meta for a job. Operator
// tooling (and the story 08 UI seam); not an MCP tool — the surface stays
// minimal per the docs-integrity rule.
func (s *Storage) ReadEvolutionEvalSet(jobID string) (train, val []EvalCase, meta EvalMeta, err error) {
	dir, err := s.evalFilesDir(jobID)
	if err != nil {
		return nil, nil, EvalMeta{}, err
	}
	readSplit := func(path string) ([]EvalCase, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var out []EvalCase
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var c EvalCase
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				return nil, fmt.Errorf("invalid stored eval split %s: %w", path, err)
			}
			out = append(out, c)
		}
		if out == nil {
			out = []EvalCase{}
		}
		return out, nil
	}
	train, err = readSplit(filepath.Join(dir, "train.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, EvalMeta{}, fmt.Errorf("no eval set stored for evolution job '%s'", jobID)
		}
		return nil, nil, EvalMeta{}, err
	}
	val, err = readSplit(filepath.Join(dir, "val.jsonl"))
	if err != nil {
		return nil, nil, EvalMeta{}, err
	}
	rawMeta, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, nil, EvalMeta{}, err
	}
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		return nil, nil, EvalMeta{}, fmt.Errorf("invalid stored eval meta for job '%s': %w", jobID, err)
	}
	return train, val, meta, nil
}

// evalSetPreview renders the stored split counts for fix-it and success
// messages without quoting case data.
func evalSetPreview(train, val int, mode string) string {
	return fmt.Sprintf("%d train / %d val (%s splits)", train, val, mode)
}
