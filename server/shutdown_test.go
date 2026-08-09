package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestShutdownEndsOpenStreams is a regression guard for a defect that silently defeated the
// graceful-shutdown fix in production.
//
// http.Server.Shutdown waits for connections to become *idle*, and an SSE stream never does. A
// single browser tab on the wiki holds one open, so shutdown blocked until its own deadline and
// the container runtime SIGKILLed the process first — leaving the Bleve index open, which is
// exactly the corruption graceful shutdown exists to prevent. Measured before the fix:
//
//	no connections        →      4ms, index closed
//	one SSE stream open   → 15,006ms, index NOT closed  (docker stop SIGKILLs at 10s)
//
// Every long-lived stream must therefore return when BeginShutdown is called.
func TestShutdownEndsOpenStreams(t *testing.T) {
	cases := []struct {
		name    string
		request func(url string) *http.Request
		serve   func(srv *Server) http.HandlerFunc
	}{
		{
			name: "browser activity stream",
			request: func(string) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/activity/stream", nil)
			},
			serve: func(srv *Server) http.HandlerFunc { return srv.HandleActivityStream },
		},
		{
			name: "MCP GET keepalive",
			request: func(string) *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
				r.Header.Set("Accept", "text/event-stream")
				r.Header.Set("Origin", "http://localhost:8080")
				return r
			},
			serve: func(srv *Server) http.HandlerFunc { return srv.HandleStreamableHTTP },
		},
		{
			name: "MCP subscriptions/listen",
			request: func(string) *http.Request {
				body := `{"jsonrpc":"2.0","id":1,"method":"subscriptions/listen","params":{"notifications":{"resourcesListChanged":true}}}`
				r := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "http://localhost:8080")
				return r
			},
			serve: func(srv *Server) http.HandlerFunc { return srv.HandleStreamableHTTP },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := resourceServer(t)

			// A cancellable request context stands in for the client connection; without the
			// shutdown signal the handler would only return when this is cancelled.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			req := tc.request("").WithContext(ctx)
			rec := httptest.NewRecorder()

			returned := make(chan struct{})
			go func() {
				defer close(returned)
				tc.serve(srv)(rec, req)
			}()

			// Let the handler reach its select loop.
			time.Sleep(100 * time.Millisecond)

			select {
			case <-returned:
				t.Fatal("stream returned before shutdown was signalled")
			default:
			}

			srv.BeginShutdown()

			select {
			case <-returned:
				// Correct: the stream ended on its own, so Shutdown can complete.
			case <-time.After(2 * time.Second):
				t.Fatal("stream ignored the shutdown signal — Shutdown would block until its " +
					"deadline and the runtime would SIGKILL before the index is closed")
			}
		})
	}
}

// TestBeginShutdownIsIdempotent pins that a second call cannot panic on a closed channel, since
// both the signal handler and any future caller may invoke it.
func TestBeginShutdownIsIdempotent(t *testing.T) {
	srv := resourceServer(t)
	srv.BeginShutdown()
	srv.BeginShutdown()
	srv.BeginShutdown()

	select {
	case <-srv.shutdownSignal():
	default:
		t.Error("shutdown signal should be closed")
	}
}

// TestSubscriptionStreamClosesGracefullyOnShutdown pins that a server-initiated end sends the
// spec's empty result, so a client can tell a deliberate close from a dropped transport.
func TestSubscriptionStreamClosesGracefullyOnShutdown(t *testing.T) {
	srv := resourceServer(t)

	body := `{"jsonrpc":"2.0","id":31,"method":"subscriptions/listen","params":{"notifications":{"resourcesListChanged":true}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.HandleStreamableHTTP(rec, req)
	}()

	time.Sleep(100 * time.Millisecond)
	srv.BeginShutdown()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end on shutdown")
	}

	out := rec.Body.String()
	if !strings.Contains(out, `"id":31`) || !strings.Contains(out, `"resultType":"complete"`) {
		t.Errorf("expected the graceful empty result closing the subscription, got:\n%s", out)
	}
}
