package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Test credentials.
//
// These are synthetic values that match the *shape* real ones have. None was ever issued and none
// authenticates anything, but they are the literal inputs the scanner has to catch, so they are
// assembled from parts rather than written out — a plausible-looking key in a source file is the
// thing repository scanners flag, and a test for a secret scanner tripping other people's secret
// scanners is a bad trade.
var (
	awsKey       = "AKIA" + strings.Repeat("Q7", 8)
	githubToken  = "ghp_" + strings.Repeat("aB3", 12)
	anthropicKey = "sk-ant-" + strings.Repeat("x9Q", 8)
	slackToken   = "xoxb-" + strings.Repeat("29", 8) + "-abcdefgh"
	googleKey    = "AIza" + strings.Repeat("Kv7", 11) + "QQ"
	jwt          = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	pemKey       = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"
	assignment   = "database_password = hunter2CorrectHorseBattery"
)

func TestScanForSecretsCatchesEachPattern(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		class string
	}{
		{"AWS access key", "The key is " + awsKey + " for the bucket.", "AWS access key ID"},
		{"GitHub token", "Use " + githubToken + " to push.", "GitHub token"},
		{"Anthropic key", "ANTHROPIC_API_KEY was " + anthropicKey, "Anthropic API key"},
		{"Slack token", "bot token " + slackToken, "Slack token"},
		{"Google key", "maps key " + googleKey, "Google API key"},
		{"JWT", "bearer " + jwt, "JSON Web Token"},
		{"PEM private key", pemKey, "private key (PEM)"},
		{"credential assignment", assignment, "credential assignment"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := ScanForSecrets("content", tc.text)
			if len(found) == 0 {
				t.Fatalf("no match for %s", tc.name)
			}
			var classes []string
			for _, m := range found {
				classes = append(classes, m.Class)
				// The offset has to actually locate the value, or it is decoration rather than
				// the thing that lets a human find it without it being quoted.
				if m.Offset < 0 || m.Offset+m.Length > len(tc.text) {
					t.Errorf("offset %d length %d is outside the text (%d bytes)", m.Offset, m.Length, len(tc.text))
				}
			}
			if !slices.Contains(classes, tc.class) {
				t.Errorf("classes = %v, want one to be %q", classes, tc.class)
			}
		})
	}
}

// TestSecretRefusalNeverQuotesTheSecret is the assertion the whole design turns on.
//
// A tool error is transcript content: it is sent to the model provider and persisted. Quoting the
// matched text to explain the refusal would reproduce exactly the exposure the check exists to
// prevent — and it is the natural thing to write when improving an error message, which is why
// this is pinned rather than left to reviewer attention.
func TestSecretRefusalNeverQuotesTheSecret(t *testing.T) {
	srv := newMCPServer(t)

	for _, secret := range []string{awsKey, githubToken, anthropicKey, slackToken, googleKey, jwt} {
		t.Run(secret[:8], func(t *testing.T) {
			body, err := jsonString("The credential is " + secret + " — do not lose it.")
			if err != nil {
				t.Fatal(err)
			}
			resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Leaky Memory","content":`+body+`,"description":"d","source":"s"}}`)
			if !resp.IsError {
				t.Fatal("the write should have been refused")
			}
			text := resp.Content[0].Text

			if strings.Contains(text, secret) {
				t.Fatal("the refusal message contains the secret verbatim")
			}
			// Not just the whole value: a message that quoted "a fragment of the key" would leak
			// it just as effectively across a couple of retries.
			if len(secret) > 12 && strings.Contains(text, secret[4:16]) {
				t.Fatal("the refusal message contains a substring of the secret")
			}
			// And it still has to be actionable, or the agent cannot tell what to fix.
			if !strings.Contains(text, "offset") {
				t.Errorf("the refusal should locate the match by offset: %s", text)
			}
		})
	}
}

func TestSecretScanRefusesEveryDocumentClass(t *testing.T) {
	srv := newMCPServer(t)

	leak, err := jsonString("deploy with " + awsKey)
	if err != nil {
		t.Fatal(err)
	}

	// A key in a plan is no safer than a key in a memory, so the check sits at one chokepoint
	// rather than on the class that happened to prompt it.
	calls := map[string]string{
		"create_wiki_article": `{"name":"create_wiki_article","arguments":{"title":"Leaky Article","content":` + leak + `}}`,
		"create_agent_memory": `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Leaky Memory","content":` + leak + `,"description":"d","source":"s"}}`,
		"create_agent_plan":   `{"name":"create_agent_plan","arguments":{"title":"Leaky Plan","content":` + leak + `,"project_context":"nexwiki"}}`,
		"create_agent_skill":  `{"name":"create_agent_skill","arguments":{"title":"Leaky Skill","content":` + leak + `}}`,
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			resp := toolCall(t, srv, call)
			if !resp.IsError {
				t.Fatalf("%s accepted a credential", name)
			}
			// Assert *which* refusal this is. create_agent_memory now has other required-field
			// gates in front of this one, so "IsError" alone would pass for the wrong reason —
			// a missing argument would satisfy it and the secret gate would never be reached.
			if !strings.Contains(resp.Content[0].Text, "AWS access key ID") {
				t.Errorf("%s was refused, but not by the secret scanner: %s", name, resp.Content[0].Text)
			}
		})
	}

	t.Run("nothing was written", func(t *testing.T) {
		for _, slug := range []string{"leaky-article", "leaky-memory", "leaky-plan", "leaky-skill"} {
			if _, err := srv.Storage.GetArticle(slug); err == nil {
				t.Errorf("%s was written despite the refusal", slug)
			}
		}
	})
}

func TestSecretScanCoversEditAndAppend(t *testing.T) {
	srv := newMCPServer(t)

	if resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Clean Memory","content":"# nothing sensitive","description":"d","source":"s"}}`); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	leak, err := jsonString("token " + githubToken)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("edit is refused", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"clean-memory","content":`+leak+`,"loaded_version":1}}`)
		if !resp.IsError {
			t.Fatal("an edit that introduces a credential must be refused")
		}
		art, _ := srv.Storage.GetArticle("clean-memory")
		if strings.Contains(art.Content, githubToken) || art.Version != 1 {
			t.Error("a refused edit must leave the document untouched")
		}
	})

	t.Run("append is refused", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"clean-memory","content_to_append":`+leak+`}}`)
		if !resp.IsError {
			t.Fatal("an append that introduces a credential must be refused")
		}
		art, _ := srv.Storage.GetArticle("clean-memory")
		if strings.Contains(art.Content, githubToken) {
			t.Error("a refused append must not have written")
		}
	})
}

// TestSecretScanChecksProvenanceFields covers the field a scanner is most likely to skip. A
// `source` is a natural home for a URL with an embedded token, and it is exactly the field an
// agent fills in with "how I learned this".
func TestSecretScanChecksProvenanceFields(t *testing.T) {
	srv := newMCPServer(t)

	resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Sourced By Token","content":"# a fact","description":"d","source":"https://ci.example.com/?token=`+githubToken+`"}}`)
	if !resp.IsError {
		t.Fatal("a credential in 'source' must be refused")
	}
	if strings.Contains(resp.Content[0].Text, githubToken) {
		t.Fatal("the refusal quoted the secret")
	}
	if !strings.Contains(resp.Content[0].Text, "source") {
		t.Errorf("the refusal should name the offending field: %s", resp.Content[0].Text)
	}
}

// TestDocumentationAboutCredentialsStaysWritable is the constraint that keeps this control alive.
// This wiki has articles about OAuth clients, token audiences and auth-service registration. A
// scanner that makes writing *about* credentials impossible gets switched off, and a control that
// is switched off protects nothing.
func TestDocumentationAboutCredentialsStaysWritable(t *testing.T) {
	srv := newMCPServer(t)

	docs := map[string]string{
		"aws-example":       "Use AWS's documented example key AKIAIOSFODNN7EXAMPLE in samples.",
		"angle-placeholder": "Set the header to `Authorization: Bearer <your-token>` before calling.",
		"brace-placeholder": "Export it as api_key = {YOUR_API_KEY} in the config file.",
		"env-reference":     "In compose: `NEXWIKI_GIT_PASSWORD=$GIT_TOKEN` — never the literal value.",
		"redaction-marker":  "The log line read `token: REDACTED` after the scrubber ran.",
		"masked-value":      "It appears in the UI as password: xxxxxxxxxxxxxxxx once masked.",
		"prose-about-keys":  "Rotate the deploy token quarterly. An AWS access key ID starts with AKIA and is twenty characters.",
	}

	for slug, body := range docs {
		t.Run(slug, func(t *testing.T) {
			content, err := jsonString(body)
			if err != nil {
				t.Fatal(err)
			}
			title, err := jsonString("Doc " + slug)
			if err != nil {
				t.Fatal(err)
			}
			resp := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":`+title+`,"content":`+content+`}}`)
			if resp.IsError {
				t.Errorf("documentation about credentials must stay writable, got: %s", resp.Content[0].Text)
			}
		})
	}
}

func TestSecretScanModes(t *testing.T) {
	leak, err := jsonString("key " + awsKey)
	if err != nil {
		t.Fatal(err)
	}
	call := `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Mode Memory","content":` + leak + `,"description":"d","source":"s"}}`

	t.Run("warn writes the document and annotates the response", func(t *testing.T) {
		t.Setenv(envSecretScan, "warn")
		srv := newMCPServer(t)
		resp := toolCall(t, srv, call)
		if resp.IsError {
			t.Fatalf("warn mode must not refuse: %s", resp.Content[0].Text)
		}
		if !strings.Contains(resp.Content[0].Text, "AWS access key ID") {
			t.Errorf("warn mode must report what it saw: %s", resp.Content[0].Text)
		}
		// Warn is a relaxation of the disposition, not of the never-echo rule.
		if strings.Contains(resp.Content[0].Text, awsKey) {
			t.Fatal("warn mode quoted the secret")
		}
		if _, err := srv.Storage.GetArticle("mode-memory"); err != nil {
			t.Error("warn mode should still have written the document")
		}
	})

	t.Run("off skips the check silently", func(t *testing.T) {
		t.Setenv(envSecretScan, "off")
		srv := newMCPServer(t)
		resp := toolCall(t, srv, call)
		if resp.IsError {
			t.Fatalf("off mode must not refuse: %s", resp.Content[0].Text)
		}
		if strings.Contains(resp.Content[0].Text, "AWS access key ID") {
			t.Errorf("off mode should say nothing: %s", resp.Content[0].Text)
		}
	})

	t.Run("an unrecognized value fails closed", func(t *testing.T) {
		// A typo in the mode must not silently disable the control — that is precisely when
		// failing open would matter most.
		t.Setenv(envSecretScan, "warn-only")
		if mode := secretScanMode(); mode != SecretScanRefuse {
			t.Errorf("mode = %q, want refuse", mode)
		}
	})

	t.Run("unset defaults to refuse", func(t *testing.T) {
		t.Setenv(envSecretScan, "")
		if mode := secretScanMode(); mode != SecretScanRefuse {
			t.Errorf("mode = %q, want refuse", mode)
		}
	})
}

// TestOKFImportIsNotABypass closes the hole a chokepoint on the tool handlers alone would leave:
// zip the credential into a bundle and import it. The importer is an MCP write path too.
func TestOKFImportIsNotABypass(t *testing.T) {
	srv := newMCPServer(t)

	bundle := buildBundle(t, map[string]string{
		"aimemories/leaked-memory.md": "---\ntype: AI-Agent-Memory\ntitle: Leaked Memory\nslug: leaked-memory\n---\nThe key is " + awsKey + "\n",
		"wiki/clean-page.md":          "---\ntype: Wiki\ntitle: Clean Page\nslug: clean-page\n---\nNothing sensitive here.\n",
	})

	report, err := srv.Storage.ImportOKFBundle(bundle)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	if _, err := srv.Storage.GetArticle("leaked-memory"); err == nil {
		t.Error("the importer wrote a document carrying a credential")
	}
	// Skipped, not aborted: one bad entry must not cost the operator the whole restore, which is
	// the same permissive posture the importer already takes toward malformed documents.
	if _, err := srv.Storage.GetArticle("clean-page"); err != nil {
		t.Error("a refused document must not abort the rest of the import")
	}
	if report.Skipped == 0 {
		t.Error("the refusal should be counted as a skip")
	}

	var warned bool
	for _, w := range report.Warnings {
		if strings.Contains(w, "AWS access key ID") {
			warned = true
		}
		if strings.Contains(w, awsKey) {
			t.Fatal("the import warning quoted the secret")
		}
	}
	if !warned {
		t.Errorf("the report should explain the refusal: %+v", report.Warnings)
	}
}

// buildBundle writes an in-memory OKF zip from path→content pairs.
func buildBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jsonString encodes a value as a JSON string literal, so a test body carrying newlines, quotes or
// a PEM block can be embedded in a tool-call argument without hand-escaping it.
func jsonString(v string) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
