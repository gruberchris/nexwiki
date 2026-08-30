package server

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Secret scanning on the MCP write path.
//
// NexWiki had no secret checking anywhere: `grep -niE "secret|redact|credential|api[_-]?key"`
// over server/ returned nothing outside comments and tests. The agent-facing rules say a memory
// must never contain credentials, and nothing checked.
//
// The exposure is specific to how this system stores things. A memory is Markdown on disk, kept
// in version history, indexed into Bleve, rendered in a web UI, and served to every connected MCP
// client. A key written once is then in the index, in the history, and in every agent's context
// on recall. There is no single place to delete it from.
//
// # Refuse, do not redact
//
// This inverts the usual disposition of an egress scanner, deliberately. A scanner sitting on a
// live output stream redacts because the turn has to continue. A *write* can safely fail: the
// caller gets a recoverable error and rewrites the document. A redacted memory is worse than a
// refused one — it reads as complete, and the hole is invisible to every later reader.
//
// # Never echo the secret
//
// A tool error is transcript content. It goes to the model provider and is persisted. Quoting the
// matched text back to explain the refusal would reproduce the exact harm the check exists to
// prevent, so a SecretMatch carries the pattern class, the byte offset and the length — and never
// the substring. The test asserts the matched text is absent from the error.

// SecretMatch is one detection: what kind of credential it looks like, and where it is. It
// deliberately has no field for the matched text — see the file comment.
type SecretMatch struct {
	// Class names the pattern that fired, e.g. "AWS access key ID".
	Class string
	// Field is the document field the match was found in: content, description, or source.
	Field string
	// Offset is the byte offset of the match within that field, and Length its byte length.
	// Together they let a human find it without the value being reproduced anywhere.
	Offset int
	Length int
}

// String renders a match for a refusal message. Offset and length only.
func (m SecretMatch) String() string {
	return fmt.Sprintf("%s in '%s' at byte offset %d (length %d)", m.Class, m.Field, m.Offset, m.Length)
}

// secretPattern pairs a compiled expression with the credential class it recognizes.
type secretPattern struct {
	class string
	re    *regexp.Regexp
}

// secretPatterns favours high-signal prefixes over generic entropy scoring. An entropy threshold
// on a *wiki* is the wrong tool: this corpus is full of hashes, slugs, base64 fragments and long
// identifiers that carry no secret, and every one of them would be a refusal a user has to argue
// with. A prefix that a vendor actually issues is a near-certain finding.
//
// Each pattern names the issuer it comes from, so a future reader can check it against that
// vendor's current format rather than guessing what it was meant to match.
var secretPatterns = []secretPattern{
	// AWS access key IDs: a fixed 4-character prefix plus 16 uppercase alphanumerics.
	{"AWS access key ID", regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16}\b`)},
	// GitHub tokens, all five current forms (personal, OAuth, user-to-server, server-to-server,
	// refresh), which share the gh?_ prefix and a 36+ character body.
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	// Slack tokens: xox followed by the token-type letter.
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
	// Anthropic API keys.
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	// OpenAI-style keys. Deliberately after the Anthropic pattern, which is a longer prefix on
	// the same shape; ordering only affects which class is *reported*, not whether it is caught.
	{"OpenAI-style API key", regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`)},
	// Google API keys.
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	// Private keys in PEM form. The header alone is conclusive.
	{"private key (PEM)", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY-----`)},
	// A JSON Web Token: three base64url segments, the first two of which decode to JSON objects.
	// Matching the literal "eyJ" prefix on the first two segments is what keeps this from firing
	// on any dotted identifier.
	{"JSON Web Token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	// Assignment-shaped credentials: `api_key = "…"`, `password: …`. The broadest pattern here,
	// and the one the placeholder allowlist below exists for.
	// The leading [A-Za-z0-9_-]* is load-bearing: `\b` before the keyword does *not* match inside
	// `database_password`, because `_` is a word character and there is no boundary there. A
	// prefixed name is the common case, so anchoring on a word boundary missed most real
	// assignments. No trailing `\b` either — the `\s*[:=]` that follows is what bounds it.
	{"credential assignment", regexp.MustCompile(`(?i)[A-Za-z0-9_-]*(?:api[_-]?key|apikey|secret|password|passwd|token|access[_-]?key)\s*[:=]\s*["']?[A-Za-z0-9/+=_-]{16,}`)},
}

// placeholderPatterns are the shapes documentation legitimately contains. This wiki documents
// credential handling — it has articles about OAuth, about auth clients, about token audiences —
// so a scanner that makes writing *about* credentials impossible would simply be turned off, and
// a control that gets turned off protects nothing.
//
// Each entry matches text that looks like a credential but demonstrably is not.
var placeholderPatterns = []*regexp.Regexp{
	// AWS's own published example key, which appears verbatim in their documentation.
	regexp.MustCompile(`AKIAIOSFODNN7EXAMPLE`),
	// Angle-bracket and brace placeholders: <your-key>, {token}, ${API_KEY}, $SECRET.
	regexp.MustCompile(`(?i)[<{$][^>}\s]*(?:your|my|the|example|placeholder|insert|api|key|token|secret|password)[^>}\s]*[>}]?`),
	// Explicit redaction markers.
	regexp.MustCompile(`(?i)\b(?:redacted|removed|elided|scrubbed|omitted|changeme|example|placeholder|dummy|sample|fake|test|xxx+|abc123|foobar|notarealkey)\b`),
	// Environment-variable references rather than values: API_KEY=$SOMETHING, token: ${VAR}.
	regexp.MustCompile(`[:=]\s*["']?\$\{?[A-Za-z_][A-Za-z0-9_]*\}?`),
}

// isPlaceholder reports whether a match is documentation rather than a credential.
//
// It tests **the matched text only**, never the surrounding prose. An earlier version widened to a
// 40-byte window on the theory that a marker like "(redacted)" sits beside the value rather than
// inside it. That was a bypass, and a broad one: `\bexample\b` is in the marker list, so a real
// credential anywhere near the string `example` — including in a hostname like `ci.example.com` —
// was silently allowed through. A test caught it. Do not reintroduce the window; if a marker
// genuinely needs to sit outside the match, extend the pattern to cover both together instead.
func isPlaceholder(text string, start, end int) bool {
	matched := text[start:end]
	for _, re := range placeholderPatterns {
		if re.MatchString(matched) {
			return true
		}
	}
	// A long run of one repeated character — xxxxxxxx, ********, 000000000000. Nobody's real
	// credential looks like this, and it is the most common way a value gets masked by hand.
	//
	// This is a function rather than a pattern in the list above because Go's regexp is RE2,
	// which has no backreferences: `(.)\1{7,}` does not compile. Worth knowing before reaching
	// for one here again.
	return hasLongCharacterRun(text[start:end], 8)
}

// hasLongCharacterRun reports whether s contains n or more of the same byte in a row.
func hasLongCharacterRun(s string, n int) bool {
	run := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			run++
			if run >= n {
				return true
			}
			continue
		}
		run = 1
	}
	return false
}

// ScanForSecrets returns every credential-shaped match in one field's text. The field name is
// carried through so a refusal can say *where* without quoting *what*.
//
// Matches are reported by class, offset and length only. Nothing in the returned value, and
// nothing in any message built from it, contains the matched text.
func ScanForSecrets(field, text string) []SecretMatch {
	if text == "" {
		return nil
	}
	var found []SecretMatch
	for _, p := range secretPatterns {
		for _, loc := range p.re.FindAllStringIndex(text, -1) {
			if isPlaceholder(text, loc[0], loc[1]) {
				continue
			}
			found = append(found, SecretMatch{
				Class:  p.class,
				Field:  field,
				Offset: loc[0],
				Length: loc[1] - loc[0],
			})
		}
	}
	return found
}

// SecretScanMode is the configured disposition for a detection.
type SecretScanMode string

const (
	// SecretScanRefuse rejects the write. The default.
	SecretScanRefuse SecretScanMode = "refuse"
	// SecretScanWarn writes the document and annotates the response. For an operator importing a
	// corpus that trips a false positive and who would rather see the report than fight the gate.
	SecretScanWarn SecretScanMode = "warn"
	// SecretScanOff skips the check entirely.
	SecretScanOff SecretScanMode = "off"
)

// envSecretScan is the mode variable, carrying the mandatory NEXWIKI_ prefix (AGENTS.md
// § Environment Variables Prefixing Rule).
const envSecretScan = "NEXWIKI_SECRET_SCAN"

// secretScanMode reads the configured disposition, defaulting to refuse.
//
// There is deliberately no per-document opt-out. A flag any agent can set on the call that is
// being checked is not a control — it is a field the caller fills in to make the check go away.
// The escape hatch is operator-level and process-wide, which is a decision a human makes once
// rather than one an agent makes per write.
//
// An unrecognized value falls back to refuse and says so. Failing closed is the right default for
// a control, and a typo in the variable name is exactly when silent failure would matter most.
func secretScanMode() SecretScanMode {
	switch mode := SecretScanMode(strings.ToLower(strings.TrimSpace(os.Getenv(envSecretScan)))); mode {
	case "":
		return SecretScanRefuse
	case SecretScanRefuse, SecretScanWarn, SecretScanOff:
		return mode
	default:
		_, _ = fmt.Fprintf(os.Stderr, "Warning: %s=%q is not one of refuse, warn, off — using refuse\n", envSecretScan, string(mode))
		return SecretScanRefuse
	}
}

// scanDocumentFields scans every field of a write that can carry free text.
//
// `source` is included because it is the natural home for a URL with an embedded token —
// "https://user:pat@git.example.com/…" is a plausible provenance value and a credential leak.
// `description` is included for the same reason at lower odds. Titles are not scanned: a slug is
// derived from the title, so a credential in one would be visible in the URL long before this,
// and the shapes above cannot survive slugification anyway.
func scanDocumentFields(content, description, source string) []SecretMatch {
	var found []SecretMatch
	found = append(found, ScanForSecrets("content", content)...)
	found = append(found, ScanForSecrets("description", description)...)
	found = append(found, ScanForSecrets("source", source)...)
	return found
}

// secretRefusal is the tool response for a write carrying a credential, or nil when the write may
// proceed. In warn mode it returns nil and the caller reports separately.
//
// The message names class, field, offset and length, and never the matched text.
func secretRefusal(docKind, content, description, source string) *ToolResponse {
	if secretScanMode() != SecretScanRefuse {
		return nil
	}
	found := scanDocumentFields(content, description, source)
	if len(found) == 0 {
		return nil
	}
	return &ToolResponse{
		IsError: true,
		Content: []ToolContent{{Type: "text", Text: secretRefusalMessage(docKind, found)}},
	}
}

// secretRefusalMessage explains the refusal in terms the caller can act on without the value
// being repeated anywhere.
func secretRefusalMessage(docKind string, found []SecretMatch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Error: refusing to write this %s — it appears to contain %s.\n\n",
		docKind, pluralizeSecrets(len(found)))
	for _, m := range found {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	b.WriteString("\nThe matched text is deliberately not repeated here: a tool error is transcript " +
		"content that is sent onward and persisted, so quoting it would reproduce the exposure this " +
		"check exists to prevent.\n\n" +
		"A NexWiki document is Markdown on disk, kept in version history, indexed for search, " +
		"rendered in the web UI, and served to every connected MCP client — so a credential written " +
		"once cannot be removed from one place.\n\n" +
		"Remove the value and describe it instead (\"the deploy token for X, in 1Password\"), or " +
		"replace it with a placeholder such as <your-token> or REDACTED, which are recognized and " +
		"allowed. If this is a false positive, an operator can set " + envSecretScan + "=warn.")
	return b.String()
}

// secretWarning returns a note to prepend to a successful response in warn mode, or "" when there
// is nothing to say. Warn mode still never quotes the match.
func secretWarning(found []SecretMatch) string {
	if len(found) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "⚠️  %s written despite what looks like %s (%s=warn):\n",
		"Document", pluralizeSecrets(len(found)), envSecretScan)
	for _, m := range found {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	b.WriteString("\n")
	return b.String()
}

// warnedSecrets returns the matches to annotate a successful write with, which is only ever
// non-empty in warn mode.
func warnedSecrets(content, description, source string) []SecretMatch {
	if secretScanMode() != SecretScanWarn {
		return nil
	}
	return scanDocumentFields(content, description, source)
}

func pluralizeSecrets(n int) string {
	if n == 1 {
		return "a credential"
	}
	return fmt.Sprintf("%d credentials", n)
}

// derefOr reads an optional string field for scanning. A nil pointer on an edit means "preserve",
// so there is no incoming value to scan — the stored one was already checked when it was written.
func derefOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
