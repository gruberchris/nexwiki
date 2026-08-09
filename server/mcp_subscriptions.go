package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	if !writeSSE(notificationEnvelope("notifications/subscriptions/acknowledged", req.ID,
		map[string]interface{}{"notifications": filter.acknowledgedFilter()})) {
		return
	}

	// A filter NexWiki cannot honor produces no events, so rather than hold a socket open
	// forever, close it gracefully: the empty result tells the client the subscription ended
	// deliberately rather than dropping.
	if !filter.active() {
		writeSSE(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]interface{}{
				"resultType": "complete",
				"_meta":      map[string]interface{}{metaSubscriptionID: req.ID},
			},
		})
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

// handleStdioSubscription answers subscriptions/listen on stdio.
//
// The stdio loop here is strictly request/response on a single channel, so a long-lived stream
// would require interleaving asynchronous notifications with responses on stdout. Rather than
// leave a stdio client waiting for notifications that will never arrive, the subscription is
// acknowledged and then closed gracefully with the empty result the spec defines for a
// server-initiated end. Streamable HTTP is the supported transport for subscriptions.
func (srv *Server) handleStdioSubscription(w io.Writer, req *JSONRPCRequest, filter subscriptionFilter) {
	ack := notificationEnvelope("notifications/subscriptions/acknowledged", req.ID,
		map[string]interface{}{"notifications": map[string]interface{}{}})
	if data, err := json.Marshal(ack); err == nil {
		_, _ = fmt.Fprintf(w, "%s\n", data)
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
