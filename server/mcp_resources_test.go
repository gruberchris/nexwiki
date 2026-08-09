package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func resourceServer(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)
	srv.EventBus = NewEventBus()
	return srv
}

// callJSON dispatches a legacy-era request and returns the decoded result object.
func callJSON(t *testing.T, srv *Server, method string, params string) map[string]interface{} {
	t.Helper()
	req := &JSONRPCRequest{JSONRPC: "2.0", Method: method, ID: 1}
	if params != "" {
		req.Params = json.RawMessage(params)
	}
	var buf bytes.Buffer
	srv.handleRequest(&buf, req)

	var envelope map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid JSON response: %v\n%s", err, buf.String())
	}
	return envelope
}

func TestResourceURIRoundTrip(t *testing.T) {
	uri := articleResourceURI("bleve-decision")
	if uri != "nexwiki://article/bleve-decision" {
		t.Errorf("unexpected URI: %s", uri)
	}

	slug, ok := slugFromResourceURI(uri)
	if !ok || slug != "bleve-decision" {
		t.Errorf("round trip failed: got (%q, %v)", slug, ok)
	}

	// A URI this server does not own must be rejected rather than coerced into a slug.
	for _, bad := range []string{"file:///etc/passwd", "nexwiki://article/", "nexwiki://other/x", "", "https://example.com"} {
		if _, ok := slugFromResourceURI(bad); ok {
			t.Errorf("slugFromResourceURI(%q) should have been rejected", bad)
		}
	}
}

func TestResourcesListIncludesEveryDocument(t *testing.T) {
	srv := resourceServer(t)
	if _, err := srv.Storage.SaveArticle("", "Bleve Decision", "why bleve", "the reason", "", "",
		"seed", nil, ContentTypeMemory); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	result := callJSON(t, srv, "resources/list", "")["result"].(map[string]interface{})
	resources := result["resources"].([]interface{})

	byURI := map[string]map[string]interface{}{}
	for _, r := range resources {
		entry := r.(map[string]interface{})
		byURI[entry["uri"].(string)] = entry
	}

	// "home" is excluded from ListArticles for the dashboard, but a user may well want to
	// @-mention it, so it must appear as a resource.
	if _, ok := byURI["nexwiki://article/home"]; !ok {
		t.Error("home should be exposed as a resource even though listings exclude it")
	}

	memory, ok := byURI["nexwiki://article/bleve-decision"]
	if !ok {
		t.Fatalf("memory not exposed as a resource; got %v", byURI)
	}
	if memory["title"] != "Bleve Decision" {
		t.Errorf("title should be the article title, got %v", memory["title"])
	}
	if memory["name"] != "bleve-decision" {
		t.Errorf("name should be the slug, got %v", memory["name"])
	}
	if memory["mimeType"] != "text/markdown" {
		t.Errorf("expected text/markdown, got %v", memory["mimeType"])
	}
	if memory["description"] != "the reason" {
		t.Errorf("description should come from the article, got %v", memory["description"])
	}
	annotations, ok := memory["annotations"].(map[string]interface{})
	if !ok || annotations["lastModified"] == nil {
		t.Errorf("resources should carry a lastModified annotation, got %v", memory["annotations"])
	}
}

func TestResourcesReadReturnsMarkdown(t *testing.T) {
	srv := resourceServer(t)
	if _, err := srv.Storage.SaveArticle("", "Read Me", "# Body\n\ncontent here", "", "", "",
		"seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	envelope := callJSON(t, srv, "resources/read", `{"uri":"nexwiki://article/read-me"}`)
	contents := envelope["result"].(map[string]interface{})["contents"].([]interface{})
	if len(contents) != 1 {
		t.Fatalf("expected one content block, got %d", len(contents))
	}
	block := contents[0].(map[string]interface{})
	if !strings.Contains(block["text"].(string), "content here") {
		t.Errorf("expected the Markdown body, got %v", block["text"])
	}
	if block["uri"] != "nexwiki://article/read-me" {
		t.Errorf("content block should echo the URI, got %v", block["uri"])
	}
}

// TestResourcesReadMissingIsInvalidParams pins the error contract. The spec forbids returning an
// empty contents array for a missing resource, because it cannot be distinguished from a resource
// that exists but is empty.
func TestResourcesReadMissingIsInvalidParams(t *testing.T) {
	srv := resourceServer(t)

	for _, uri := range []string{"nexwiki://article/does-not-exist", "file:///etc/passwd"} {
		envelope := callJSON(t, srv, "resources/read", `{"uri":"`+uri+`"}`)
		errObj, ok := envelope["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: expected an error, got %v", uri, envelope)
		}
		if int(errObj["code"].(float64)) != errCodeInvalidParams {
			t.Errorf("%s: expected -32602, got %v", uri, errObj["code"])
		}
		data, ok := errObj["data"].(map[string]interface{})
		if !ok || data["uri"] != uri {
			t.Errorf("%s: error data should echo the URI, got %v", uri, errObj["data"])
		}
		if _, present := envelope["result"]; present {
			t.Errorf("%s: a missing resource must not return a result", uri)
		}
	}
}

func TestResourceTemplatesAdvertiseTheURIShape(t *testing.T) {
	srv := resourceServer(t)
	result := callJSON(t, srv, "resources/templates/list", "")["result"].(map[string]interface{})
	templates := result["resourceTemplates"].([]interface{})
	if len(templates) != 1 {
		t.Fatalf("expected one template, got %d", len(templates))
	}
	if templates[0].(map[string]interface{})["uriTemplate"] != "nexwiki://article/{slug}" {
		t.Errorf("unexpected template: %v", templates[0])
	}
}

// --- subscriptions ------------------------------------------------------------------------------

func TestSubscriptionFilterHonorsOnlyWhatIsServed(t *testing.T) {
	requested := subscriptionFilter{
		ToolsListChanged:      true,
		PromptsListChanged:    true,
		ResourcesListChanged:  true,
		ResourceSubscriptions: []string{"nexwiki://article/go"},
	}
	honored := requested.honored()

	// Tools and prompts are compiled in and cannot change while the process runs, so promising
	// those notifications would leave a client waiting for something that can never arrive.
	if honored.ToolsListChanged || honored.PromptsListChanged {
		t.Error("static tool/prompt lists must not be acknowledged as subscribable")
	}
	if !honored.ResourcesListChanged || len(honored.ResourceSubscriptions) != 1 {
		t.Errorf("resource notifications should be honored, got %+v", honored)
	}

	ack := honored.acknowledgedFilter()
	if _, present := ack["toolsListChanged"]; present {
		t.Error("acknowledgment must omit types the server does not deliver")
	}
}

func TestWikiUpdateMapsToNotifications(t *testing.T) {
	watched := articleResourceURI("go")
	filter := subscriptionFilter{ResourcesListChanged: true, ResourceSubscriptions: []string{watched}}

	methods := func(u WikiUpdate, f subscriptionFilter) []string {
		var out []string
		for _, n := range wikiUpdateNotifications(u, f, 7) {
			out = append(out, n["method"].(string))
			meta := n["params"].(map[string]interface{})["_meta"].(map[string]interface{})
			if meta[metaSubscriptionID] != 7 {
				t.Errorf("every message must carry the subscription ID, got %v", meta)
			}
		}
		return out
	}

	// Editing a watched article notifies that resource.
	if got := methods(WikiUpdate{Type: "article-edited", Slug: "go"}, filter); len(got) != 1 ||
		got[0] != "notifications/resources/updated" {
		t.Errorf("edit of a watched article: got %v", got)
	}

	// Creating one changes which resources exist.
	if got := methods(WikiUpdate{Type: "article-added", Slug: "new-page"}, filter); len(got) != 1 ||
		got[0] != "notifications/resources/list_changed" {
		t.Errorf("create: got %v", got)
	}

	// An edit to an article nobody subscribed to produces nothing — the server MUST NOT send
	// notification types (or targets) the client did not ask for.
	if got := methods(WikiUpdate{Type: "article-edited", Slug: "unwatched"}, filter); len(got) != 0 {
		t.Errorf("unwatched edit should produce no notifications, got %v", got)
	}

	// A filter that asked for nothing gets nothing, even for a watched slug.
	if got := methods(WikiUpdate{Type: "article-edited", Slug: "go"}, subscriptionFilter{}); len(got) != 0 {
		t.Errorf("empty filter should produce no notifications, got %v", got)
	}
}

// TestSubscriptionStreamDeliversLiveUpdates is the end-to-end proof of the feature: an open
// subscription receives an acknowledgment first, then a notification generated by an actual
// article edit published through the EventBus.
//
// This runs against a real httptest server rather than a ResponseRecorder. A recorder is not
// safe for concurrent use, so reading it while the handler goroutine is still streaming is a
// data race — and reading only "whatever arrived by now" would make the assertions timing
// dependent. Reading the live response body instead lets each step block until the message it
// needs has actually arrived, which is both race-free and deterministic.
func TestSubscriptionStreamDeliversLiveUpdates(t *testing.T) {
	srv := resourceServer(t)

	httpSrv := httptest.NewServer(http.HandlerFunc(srv.HandleStreamableHTTP))
	defer httpSrv.Close()

	body := `{"jsonrpc":"2.0","id":42,"method":"subscriptions/listen","params":{"notifications":{` +
		`"resourcesListChanged":true,"resourceSubscriptions":["nexwiki://article/live-page"]}}}`
	req, err := http.NewRequest(http.MethodPost, httpSrv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("subscriptions/listen must respond with an SSE stream, got %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("stream must disable proxy buffering or notifications arrive in batches")
	}

	// nextMessage blocks until the stream yields one JSON-RPC message, so the test never races
	// the writer or guesses at timing.
	scanner := bufio.NewScanner(resp.Body)
	nextMessage := func() map[string]interface{} {
		t.Helper()
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue // SSE comment (keep-alive) or blank separator
			}
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &msg); err != nil {
				t.Fatalf("stream emitted invalid JSON: %v", err)
			}
			return msg
		}
		t.Fatalf("stream closed before the expected message arrived: %v", scanner.Err())
		return nil
	}

	assertSubscriptionID := func(msg map[string]interface{}) {
		t.Helper()
		meta := msg["params"].(map[string]interface{})["_meta"].(map[string]interface{})
		if meta[metaSubscriptionID] != float64(42) {
			t.Errorf("message carried subscription ID %v, want 42", meta[metaSubscriptionID])
		}
	}

	// The acknowledgment MUST arrive first; no notification may precede it.
	ack := nextMessage()
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("first message must be the acknowledgment, got %v", ack["method"])
	}
	assertSubscriptionID(ack)

	// Only now publish — the stream is provably registered with the bus.
	srv.EventBus.PublishWikiUpdate(WikiUpdate{Type: "article-edited", Slug: "live-page", Title: "Live Page"})
	updated := nextMessage()
	if updated["method"] != "notifications/resources/updated" {
		t.Fatalf("expected resources/updated, got %v", updated["method"])
	}
	if uri := updated["params"].(map[string]interface{})["uri"]; uri != "nexwiki://article/live-page" {
		t.Errorf("notification carried uri %v", uri)
	}
	assertSubscriptionID(updated)

	srv.EventBus.PublishWikiUpdate(WikiUpdate{Type: "article-added", Slug: "another", Title: "Another"})
	listChanged := nextMessage()
	if listChanged["method"] != "notifications/resources/list_changed" {
		t.Fatalf("expected resources/list_changed, got %v", listChanged["method"])
	}
	assertSubscriptionID(listChanged)

	// An edit to an article nobody subscribed to must produce nothing. Publishing it before a
	// watched edit means the next message read proves the unwatched one was skipped rather than
	// merely slow.
	srv.EventBus.PublishWikiUpdate(WikiUpdate{Type: "article-edited", Slug: "unwatched", Title: "Unwatched"})
	srv.EventBus.PublishWikiUpdate(WikiUpdate{Type: "article-edited", Slug: "live-page", Title: "Live Page"})
	if next := nextMessage(); next["method"] != "notifications/resources/updated" {
		t.Errorf("unwatched edit should have been skipped; got %v", next["method"])
	}
}

// TestSubscriptionWithNothingHonoredClosesGracefully pins that a client asking only for
// notifications NexWiki cannot deliver is told so, rather than left holding an idle socket.
func TestSubscriptionWithNothingHonoredClosesGracefully(t *testing.T) {
	srv := resourceServer(t)

	body := `{"jsonrpc":"2.0","id":9,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	srv.HandleStreamableHTTP(rec, req)

	out := rec.Body.String()
	if !strings.Contains(out, "notifications/subscriptions/acknowledged") {
		t.Errorf("expected an acknowledgment, got:\n%s", out)
	}
	// The graceful-closure response is the JSON-RPC reply to the long-lived request.
	if !strings.Contains(out, `"id":9`) || !strings.Contains(out, `"resultType":"complete"`) {
		t.Errorf("expected a graceful empty result closing the subscription, got:\n%s", out)
	}
}
