package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// subscriptions/listen — long-lived server-to-client notification streams.
//
// This is what makes NexWiki a *subscribable* knowledge base rather than one an agent has to
// re-poll: an agent holding a subscription learns the moment you edit a page in the browser, or
// another agent writes a memory. The events already existed — the EventBus has been driving the
// browser's live activity drawer all along — so this wires an existing signal to a second consumer
// rather than inventing one.
//
// Note the 2026-07-28 shape: subscriptions/listen replaced both the old `resources/subscribe` RPC
// and the standalone HTTP GET stream. The response to this one request *is* the stream.

// subscriptionFilter is the set of notification types a client asked for. The server MUST NOT send
// a type the client did not request, so every delivery path checks this first.
type subscriptionFilter struct {
	ToolsListChanged      bool     `json:"toolsListChanged"`
	PromptsListChanged    bool     `json:"promptsListChanged"`
	ResourcesListChanged  bool     `json:"resourcesListChanged"`
	ResourceSubscriptions []string `json:"resourceSubscriptions"`
}

// subscriptionParams is the params object of a subscriptions/listen request.
type subscriptionParams struct {
	Notifications subscriptionFilter `json:"notifications"`
}

// honored narrows a requested filter to what NexWiki actually delivers, which is what the
// acknowledgment must report.
//
// toolsListChanged and promptsListChanged are deliberately dropped: NexWiki's tool and prompt sets
// are compiled in and cannot change while the process runs, so acknowledging them would promise a
// notification that can never arrive. A client is better served knowing that up front — it can
// stop waiting — than being left subscribed to silence.
func (f subscriptionFilter) honored() subscriptionFilter {
	return subscriptionFilter{
		ResourcesListChanged:  f.ResourcesListChanged,
		ResourceSubscriptions: f.ResourceSubscriptions,
	}
}

// wants reports whether a specific resource URI was subscribed to.
func (f subscriptionFilter) wants(uri string) bool {
	for _, subscribed := range f.ResourceSubscriptions {
		if subscribed == uri {
			return true
		}
	}
	return false
}

// active reports whether the honored filter delivers anything at all.
func (f subscriptionFilter) active() bool {
	return f.ResourcesListChanged || len(f.ResourceSubscriptions) > 0
}

// acknowledgedFilter renders the honored filter for the acknowledgment payload, omitting fields
// that were not requested so the client sees exactly what it will receive.
func (f subscriptionFilter) acknowledgedFilter() map[string]interface{} {
	out := map[string]interface{}{}
	if f.ResourcesListChanged {
		out["resourcesListChanged"] = true
	}
	if len(f.ResourceSubscriptions) > 0 {
		out["resourceSubscriptions"] = f.ResourceSubscriptions
	}
	return out
}

// notificationEnvelope builds a JSON-RPC notification carrying the subscription ID, which every
// message on the stream must include so a client can demultiplex concurrent subscriptions.
func notificationEnvelope(method string, subscriptionID interface{}, params map[string]interface{}) map[string]interface{} {
	if params == nil {
		params = map[string]interface{}{}
	}
	params["_meta"] = map[string]interface{}{metaSubscriptionID: subscriptionID}
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
}

// subscriptionAck builds the acknowledgment that MUST be the first message on a subscription,
// reporting the subset of the requested filter the server actually agreed to honor.
func subscriptionAck(subscriptionID interface{}, filter subscriptionFilter) map[string]interface{} {
	return notificationEnvelope("notifications/subscriptions/acknowledged", subscriptionID,
		map[string]interface{}{"notifications": filter.acknowledgedFilter()})
}

// subscriptionClosed builds the response to the long-lived subscriptions/listen request itself.
//
// Sending it is what distinguishes a subscription the server ended deliberately from a transport
// that simply dropped: the former carries a JSON-RPC response correlated by id, the latter carries
// nothing. It goes through the same envelope every other modern result does, so it carries
// serverInfo alongside the subscription id rather than being the one result that identifies nobody.
func (srv *Server) subscriptionClosed(subscriptionID interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      subscriptionID,
		"result": map[string]interface{}{
			"resultType": "complete",
			"_meta": map[string]interface{}{
				metaSubscriptionID: subscriptionID,
				metaServerInfo:     srv.implementation(),
			},
		},
	}
}

// wikiUpdateNotifications maps one article change onto the notifications a filter asked for.
//
// An edit changes a document's contents; a create or delete changes which documents exist. A
// rename is both, but the EventBus reports it as an edit of the new slug, so the list-changed
// signal for renames rides on the create/remove events its callers also publish.
func wikiUpdateNotifications(update WikiUpdate, filter subscriptionFilter, subscriptionID interface{}) []map[string]interface{} {
	var out []map[string]interface{}

	uri := articleResourceURI(update.Slug)
	if update.Slug != "" && filter.wants(uri) {
		out = append(out, notificationEnvelope("notifications/resources/updated", subscriptionID,
			map[string]interface{}{"uri": uri}))
	}

	if filter.ResourcesListChanged && (update.Type == "article-added" || update.Type == "article-removed") {
		out = append(out, notificationEnvelope("notifications/resources/list_changed", subscriptionID, nil))
	}

	return out
}

// streamSubscription writes the SSE stream that *is* the response to a subscriptions/listen
// request: an acknowledgment, then notifications until the client disconnects or the server stops.
//
// The acknowledgment MUST come first and no notification may precede it, so it is written before
// the EventBus channel is drained.
func (srv *Server) streamSubscription(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest, filter subscriptionFilter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Without this, nginx buffers the stream and the "live" knowledge base is anything but.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeSSE := func(payload interface{}) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Acknowledgment first, always.
	if !writeSSE(subscriptionAck(req.ID, filter)) {
		return
	}

	// A filter NexWiki cannot honor produces no events, so rather than hold a socket open
	// forever, close it gracefully: the empty result tells the client the subscription ended
	// deliberately rather than dropping.
	if !filter.active() {
		writeSSE(srv.subscriptionClosed(req.ID))
		return
	}

	if srv.EventBus == nil {
		return
	}
	updates := srv.EventBus.SubscribeWikiUpdates()
	defer srv.EventBus.UnsubscribeWikiUpdates(updates)

	// Comment-only keep-alives stop intermediaries and idle timeouts from closing a quiet stream.
	// Per the SSE spec a leading colon is a comment carrying no event data.
	keepAlive := time.NewTicker(subscriptionKeepAlive)
	defer keepAlive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client closed the stream — that is the cancellation signal on Streamable HTTP.
			return

		case <-srv.shutdownSignal():
			// Server-initiated end: reply to the long-lived request with the empty result the
			// spec defines, so the client knows the subscription closed deliberately rather than
			// the transport dropping.
			writeSSE(srv.subscriptionClosed(req.ID))
			return

		case update, ok := <-updates:
			if !ok {
				return
			}
			for _, notification := range wikiUpdateNotifications(update, filter, req.ID) {
				if !writeSSE(notification) {
					return
				}
			}

		case <-keepAlive.C:
			if _, err := io.WriteString(w, ":\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// subscriptionKeepAlive is how often a quiet stream emits an SSE comment.
const subscriptionKeepAlive = 25 * time.Second

// syncLineWriter serializes newline-delimited JSON-RPC messages onto a single output channel.
//
// stdio has exactly one stdout shared by everything: request responses written by the read loop and
// subscription notifications written by their own goroutines. Without a lock those interleave
// mid-line and corrupt the JSON-RPC stream — the same class of failure the log-to-stderr rule exists
// to prevent, and the reason the sidecar proxy already carries a writer lock of its own.
//
// Write is deliberately the io.Writer implementation rather than a wrapper: fmt.Fprintf formats
// into a buffer and issues a single Write, so one response is one locked call and cannot be split.
type syncLineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSyncLineWriter(w io.Writer) *syncLineWriter {
	return &syncLineWriter{w: w}
}

func (s *syncLineWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// writeMessage marshals one JSON-RPC message and emits it as a single line, reporting whether the
// channel is still usable.
func (s *syncLineWriter) writeMessage(payload interface{}) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(s, "%s\n", data)
	return err == nil
}

// writeJSONLine emits one JSON-RPC message to an arbitrary writer, for the paths that have no
// shared channel to serialize.
func writeJSONLine(w io.Writer, payload interface{}) {
	if data, err := json.Marshal(payload); err == nil {
		_, _ = fmt.Fprintf(w, "%s\n", data)
	}
}

// handleStdioSubscription answers subscriptions/listen on stdio.
//
// stdio gets real subscriptions, not a stub. The specification defines
// `io.modelcontextprotocol/subscriptionId` precisely because stdio multiplexes every subscription
// onto one channel, so the shape is supported by design — what it needs is a serialized writer and
// a goroutine per subscription, which is what syncLineWriter and the loop below provide.
//
// Two things were wrong before. The stream was never opened at all, so a stdio client subscribed to
// silence; and, worse, the long-lived request was never answered — the acknowledgment is a
// *notification*, carrying no id, so the client sat waiting on a response that was never coming and
// only a timeout ended it. Every exit path below now writes the closure response.
func (srv *Server) handleStdioSubscription(w io.Writer, req *JSONRPCRequest, filter subscriptionFilter) {
	honored := filter.honored()
	writeJSONLine(w, subscriptionAck(req.ID, honored))

	// No shared channel (a caller dispatching straight into a buffer), no event source, or nothing
	// to deliver: close immediately rather than leave the request hanging.
	out := srv.stdioOut
	if out == nil || srv.EventBus == nil || !honored.active() {
		writeJSONLine(w, srv.subscriptionClosed(req.ID))
		return
	}

	updates := srv.EventBus.SubscribeWikiUpdates()
	ctx := srv.registerStdioSubscription(req.ID)

	go func() {
		defer srv.EventBus.UnsubscribeWikiUpdates(updates)
		defer srv.unregisterStdioSubscription(req.ID)

		for {
			select {
			case <-ctx.Done():
				// notifications/cancelled from the client, which is how cancellation is signalled
				// on stdio: there is no per-request stream to close.
				out.writeMessage(srv.subscriptionClosed(req.ID))
				return

			case <-srv.shutdownSignal():
				out.writeMessage(srv.subscriptionClosed(req.ID))
				return

			case update, ok := <-updates:
				if !ok {
					out.writeMessage(srv.subscriptionClosed(req.ID))
					return
				}
				for _, notification := range wikiUpdateNotifications(update, honored, req.ID) {
					if !out.writeMessage(notification) {
						return
					}
				}
			}
		}
	}()
}

// subscriptionKey renders a JSON-RPC id as a map key. Ids arrive as float64 from encoding/json but
// may be strings, and a cancellation names the same id the request used, so both sides normalize
// the same way.
func subscriptionKey(id interface{}) string {
	return fmt.Sprintf("%v", id)
}

// registerStdioSubscription records a live stdio subscription and returns the context that ends it.
func (srv *Server) registerStdioSubscription(id interface{}) context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	srv.stdioSubsMu.Lock()
	defer srv.stdioSubsMu.Unlock()
	if srv.stdioSubs == nil {
		srv.stdioSubs = map[string]context.CancelFunc{}
	}
	key := subscriptionKey(id)
	// A client reusing an id replaces the older subscription rather than orphaning its goroutine.
	if previous, exists := srv.stdioSubs[key]; exists {
		previous()
	}
	srv.stdioSubs[key] = cancel
	return ctx
}

// unregisterStdioSubscription drops a finished subscription from the registry.
func (srv *Server) unregisterStdioSubscription(id interface{}) {
	srv.stdioSubsMu.Lock()
	defer srv.stdioSubsMu.Unlock()
	delete(srv.stdioSubs, subscriptionKey(id))
}

// cancelStdioSubscription ends the subscription a notifications/cancelled names, if it is one of
// ours. An unknown id is ignored: the specification requires a cancellation for an already-finished
// request to be tolerated silently, since it races with the response by nature.
func (srv *Server) cancelStdioSubscription(params json.RawMessage) {
	var cancelled struct {
		RequestID interface{} `json:"requestId"`
	}
	if len(params) == 0 || json.Unmarshal(params, &cancelled) != nil || cancelled.RequestID == nil {
		return
	}

	srv.stdioSubsMu.Lock()
	cancel, found := srv.stdioSubs[subscriptionKey(cancelled.RequestID)]
	srv.stdioSubsMu.Unlock()

	if found {
		cancel()
	}
}

// parseSubscriptionParams decodes the notification filter from a subscriptions/listen request.
func parseSubscriptionParams(params json.RawMessage) subscriptionFilter {
	var parsed subscriptionParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &parsed)
	}
	return parsed.Notifications
}
