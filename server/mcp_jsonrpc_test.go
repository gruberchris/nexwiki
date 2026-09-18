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

// jsonrpcReply is a JSON-RPC response decoded without losing its id: decoding the id into interface{}
// would round it through float64, hiding exactly the corruption these tests look for.
type jsonrpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *JSONRPCError   `json:"error"`
}

// modernMeta is the per-request metadata that opts a params object into the 2026-07-28 era.
const modernMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
	`"io.modelcontextprotocol/clientCapabilities":{}}`

// modernHeaders mirrors the metadata a modern request must carry over HTTP.
func modernHeaders(method string) http.Header {
	return http.Header{"Mcp-Protocol-Version": {ModernProtocolVersion}, "Mcp-Method": {method}}
}

// postJSONRPC sends one body through the Streamable HTTP handler, returning the status and body.
func postJSONRPC(t *testing.T, srv *Server, body string, headers http.Header) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for name, values := range headers {
		req.Header[name] = values
	}
	w := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w, req)
	return w.Code, w.Body.String()
}

// sendStdio runs the stdio loop over a single line and returns everything it wrote.
func sendStdio(t *testing.T, srv *Server, line string) string {
	t.Helper()
	var out bytes.Buffer
	NewStdioMCPServer(srv, strings.NewReader(line+"\n"), &out).Serve()
	return out.String()
}

// TestJSONRPCEnvelope pins how the MCP endpoint treats the JSON-RPC envelope, on both transports.
//
// Ids used to be decoded into interface{}, so every number became a float64: 12345678901234567890
// came back as 12345678901234567000 and the client could not match the reply to its request. And
// every decode failure was reported as -32700 with a null id, so a batch — valid JSON — was called
// a parse error, and an object with a readable id but a wrongly typed member lost that id. `"id":
// null` was taken for a notification and silently dropped, though MCP forbids null ids, as was a
// bare null. Member names were matched case-insensitively, so "ID" counted as the id.
func TestJSONRPCEnvelope(t *testing.T) {
	srv := newMCPServer(t)

	const (
		success      = 0
		notification = 1
	)
	cases := []struct {
		name    string
		body    string
		headers http.Header // HTTP only; stdio carries no headers
		code    int         // success, notification, or the JSON-RPC error code expected
		id      string      // the exact id JSON expected on the reply
		status  int         // HTTP status expected
	}{
		{name: "large integer id round-trips exactly",
			body: `{"jsonrpc":"2.0","id":12345678901234567890,"method":"tools/list"}`,
			code: success, id: `12345678901234567890`, status: http.StatusOK},
		{name: "integer id wider than any Go integer round-trips exactly",
			body: `{"jsonrpc":"2.0","id":123456789012345678901234567890,"method":"tools/list"}`,
			code: success, id: `123456789012345678901234567890`, status: http.StatusOK},
		{name: "string id",
			body: `{"jsonrpc":"2.0","id":"req-7 ü","method":"tools/list"}`,
			code: success, id: `"req-7 ü"`, status: http.StatusOK},
		{name: "escaped string id round-trips byte-exact",
			body: `{"jsonrpc":"2.0","id":"a\u0041\tb","method":"tools/list"}`,
			code: success, id: `"a\u0041\tb"`, status: http.StatusOK},
		{name: "negative id past float64 precision",
			body: `{"jsonrpc":"2.0","id":-9007199254740993,"method":"tools/list"}`,
			code: success, id: `-9007199254740993`, status: http.StatusOK},
		{name: "zero id",
			body: `{"jsonrpc":"2.0","id":0,"method":"tools/list"}`,
			code: success, id: `0`, status: http.StatusOK},
		{name: "modern era large id round-trips exactly",
			body:    `{"jsonrpc":"2.0","id":12345678901234567890,"method":"tools/list","params":{` + modernMeta + `}}`,
			headers: modernHeaders("tools/list"),
			code:    success, id: `12345678901234567890`, status: http.StatusOK},
		{name: "modern era error keeps the exact id",
			body:    `{"jsonrpc":"2.0","id":12345678901234567891,"method":"no/such","params":{` + modernMeta + `}}`,
			headers: modernHeaders("no/such"),
			code:    errCodeMethodNotFound, id: `12345678901234567891`, status: http.StatusNotFound},

		{name: "batch is an invalid request",
			body: `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "batch behind leading whitespace is still a batch",
			body: `   [{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "empty batch is an invalid request",
			body: `[]`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "wrongly typed method keeps the id",
			body: `{"jsonrpc":"2.0","id":7,"method":42}`,
			code: errCodeInvalidRequest, id: `7`, status: http.StatusBadRequest},
		{name: "null method keeps the id",
			body: `{"jsonrpc":"2.0","id":"m","method":null}`,
			code: errCodeInvalidRequest, id: `"m"`, status: http.StatusBadRequest},
		{name: "missing method keeps the id",
			body: `{"jsonrpc":"2.0","id":8}`,
			code: errCodeInvalidRequest, id: `8`, status: http.StatusBadRequest},
		{name: "wrong jsonrpc version keeps the id",
			body: `{"jsonrpc":"1.0","id":9,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `9`, status: http.StatusBadRequest},
		{name: "missing jsonrpc version keeps the id",
			body: `{"id":10,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `10`, status: http.StatusBadRequest},
		{name: "non-string jsonrpc version keeps the id",
			body: `{"jsonrpc":2.0,"id":11,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `11`, status: http.StatusBadRequest},
		{name: "modern era invalid request keeps the id",
			body:    `{"jsonrpc":"2.0","id":12,"method":42,"params":{` + modernMeta + `}}`,
			headers: modernHeaders("tools/list"),
			code:    errCodeInvalidRequest, id: `12`, status: http.StatusBadRequest},
		{name: "invalid request without an id is still answered",
			body: `{"jsonrpc":"2.0","method":42}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},

		{name: "null id is an invalid request, not a notification",
			body: `{"jsonrpc":"2.0","id":null,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "modern era null id is an invalid request",
			body:    `{"jsonrpc":"2.0","id":null,"method":"tools/list","params":{` + modernMeta + `}}`,
			headers: modernHeaders("tools/list"),
			code:    errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "fractional id",
			body: `{"jsonrpc":"2.0","id":1.5,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "exponent id",
			body: `{"jsonrpc":"2.0","id":1e3,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "boolean id",
			body: `{"jsonrpc":"2.0","id":true,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "object id",
			body: `{"jsonrpc":"2.0","id":{"n":1},"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "array id",
			body: `{"jsonrpc":"2.0","id":[1],"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "invalid id alongside an invalid method",
			body: `{"jsonrpc":"2.0","id":2.5,"method":42}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "top-level number",
			body: `42`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "top-level string",
			body: `"tools/list"`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "top-level null",
			body: `null`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "top-level boolean",
			body: `true`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},

		// Member names are case-sensitive: a wrong-case member is an unknown one, not the member.
		{name: "all-uppercase members are not the request members",
			body: `{"ID":7,"METHOD":"tools/list","JSONRPC":"2.0"}`,
			code: errCodeInvalidRequest, id: `null`, status: http.StatusBadRequest},
		{name: "wrong-case method is a missing method",
			body: `{"jsonrpc":"2.0","id":7,"Method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `7`, status: http.StatusBadRequest},
		{name: "wrong-case jsonrpc is a missing version",
			body: `{"Jsonrpc":"2.0","id":7,"method":"tools/list"}`,
			code: errCodeInvalidRequest, id: `7`, status: http.StatusBadRequest},
		{name: "wrong-case id is no id, so the message is a notification",
			body: `{"jsonrpc":"2.0","ID":7,"method":"tools/list"}`,
			code: notification, status: http.StatusAccepted},

		{name: "message without an id is a notification",
			body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			code: notification, status: http.StatusAccepted},
		{name: "modern era message without an id is a notification",
			body:    `{"jsonrpc":"2.0","method":"notifications/initialized","params":{` + modernMeta + `}}`,
			headers: modernHeaders("notifications/initialized"),
			code:    notification, status: http.StatusAccepted},

		{name: "unparseable JSON is a parse error",
			body: `{"jsonrpc":"2.0","id":1,`,
			code: errCodeParseError, id: `null`, status: http.StatusBadRequest},
		{name: "trailing garbage is a parse error",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/list"} x`,
			code: errCodeParseError, id: `null`, status: http.StatusBadRequest},
	}

	check := func(t *testing.T, body string, code int, wantID string) {
		t.Helper()
		if code == notification {
			if strings.TrimSpace(body) != "" {
				t.Errorf("a notification must get no response, got %.300s", body)
			}
			return
		}
		if strings.Count(strings.TrimSpace(body), "\n") != 0 {
			t.Fatalf("expected exactly one response, got:\n%.300s", body)
		}
		var reply jsonrpcReply
		if err := json.Unmarshal([]byte(body), &reply); err != nil {
			t.Fatalf("response is not a JSON-RPC object: %v\n%.300s", err, body)
		}
		if string(reply.ID) != wantID {
			t.Errorf("id = %s, want %s", reply.ID, wantID)
		}
		if code == success {
			if reply.Error != nil || len(reply.Result) == 0 {
				t.Errorf("expected a result, got %.300s", body)
			}
			return
		}
		if reply.Error == nil {
			t.Fatalf("expected error %d, got %.300s", code, body)
		}
		if reply.Error.Code != code {
			t.Errorf("error code = %d, want %d (%s)", reply.Error.Code, code, reply.Error.Message)
		}
	}

	for _, tc := range cases {
		t.Run("http/"+tc.name, func(t *testing.T) {
			status, body := postJSONRPC(t, srv, tc.body, tc.headers)
			if status != tc.status {
				t.Errorf("status = %d, want %d (%.300s)", status, tc.status, strings.TrimSpace(body))
			}
			check(t, body, tc.code, tc.id)
		})
		t.Run("stdio/"+tc.name, func(t *testing.T) {
			check(t, sendStdio(t, srv, tc.body), tc.code, tc.id)
		})
	}
}

// TestJSONRPCBatchErrorNamesBatches pins that the batch rejection says why, rather than leaving a
// client to guess what was invalid about a well-formed request inside the array.
func TestJSONRPCBatchErrorNamesBatches(t *testing.T) {
	srv := newMCPServer(t)
	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`

	_, httpBody := postJSONRPC(t, srv, body, nil)
	for transport, out := range map[string]string{"http": httpBody, "stdio": sendStdio(t, srv, body)} {
		var reply jsonrpcReply
		if err := json.Unmarshal([]byte(out), &reply); err != nil || reply.Error == nil {
			t.Fatalf("%s: expected one error response, got %s", transport, out)
		}
		if !strings.Contains(strings.ToLower(reply.Error.Message), "batch") {
			t.Errorf("%s: message should say batches are not supported, got %q", transport, reply.Error.Message)
		}
	}
}

// TestSidecarAgreesWithPrimaryOnEnvelopes runs envelopes through the -mcp-only sidecar, which
// relays the primary's reply verbatim and reads only an exact-case "id" itself. The two must agree
// on which messages are notifications, and the relayed id must survive both hops exactly.
func TestSidecarAgreesWithPrimaryOnEnvelopes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string // the exact line the stdio client should receive, "" for none
	}{
		{"large id survives both hops",
			`{"jsonrpc":"2.0","id":12345678901234567890,"method":"no/such"}`,
			`{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found: no/such"},"id":12345678901234567890}`},
		{"wrong-case id is a notification to both",
			`{"jsonrpc":"2.0","ID":7,"method":"tools/list"}`, ""},
		{"null id is answered, not dropped",
			`{"jsonrpc":"2.0","id":null,"method":"tools/list"}`,
			`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request: \"id\" must be a string or an integer"},"id":null}`},
		{"bare null is answered, not dropped",
			`null`,
			`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request: expected a request object"},"id":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, proxy, out := proxyAgainstPrimary(t)
			proxy.Run(strings.NewReader(tc.line + "\n"))
			if got := strings.TrimSpace(out.String()); got != tc.want {
				t.Errorf("sidecar wrote:\n%.300s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestSubscriptionIDRoundTrips pins the exact id on a subscription stream, where it is echoed twice:
// as the stream's subscription id on every message, and as the id of the reply that closes it.
func TestSubscriptionIDRoundTrips(t *testing.T) {
	srv := resourceServer(t)
	const id = `12345678901234567890`
	// Asking only for notifications NexWiki cannot deliver closes the stream at once, so the whole
	// exchange is finite.
	body := `{"jsonrpc":"2.0","id":` + id + `,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true}}}`

	type message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		} `json:"params"`
		Result struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		} `json:"result"`
	}
	decode := func(t *testing.T, line string) message {
		t.Helper()
		var m message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stream line is not JSON: %v\n%s", err, line)
		}
		return m
	}

	t.Run("http", func(t *testing.T) {
		_, out := postJSONRPC(t, srv, body, nil)
		var messages []message
		scanner := bufio.NewScanner(strings.NewReader(out))
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				messages = append(messages, decode(t, data))
			}
		}
		if len(messages) != 2 {
			t.Fatalf("expected an acknowledgment and a closing result, got:\n%s", out)
		}
		if got := string(messages[0].Params.Meta[metaSubscriptionID]); got != id {
			t.Errorf("acknowledgment subscription id = %s, want %s", got, id)
		}
		if got := string(messages[1].ID); got != id {
			t.Errorf("closing result id = %s, want %s", got, id)
		}
		if got := string(messages[1].Result.Meta[metaSubscriptionID]); got != id {
			t.Errorf("closing result subscription id = %s, want %s", got, id)
		}
	})

	t.Run("stdio", func(t *testing.T) {
		out := strings.TrimSpace(sendStdio(t, srv, body))
		lines := strings.Split(out, "\n")
		if len(lines) != 2 {
			t.Fatalf("expected an acknowledgment and a closing result, got:\n%s", out)
		}
		ack := decode(t, strings.TrimSpace(lines[0]))
		if ack.Method != "notifications/subscriptions/acknowledged" {
			t.Fatalf("expected the acknowledgment, got method %q", ack.Method)
		}
		if got := string(ack.Params.Meta[metaSubscriptionID]); got != id {
			t.Errorf("acknowledgment subscription id = %s, want %s", got, id)
		}
		closing := decode(t, strings.TrimSpace(lines[1]))
		if got := string(closing.ID); got != id {
			t.Errorf("closing result id = %s, want %s", got, id)
		}
		if got := string(closing.Result.Meta[metaSubscriptionID]); got != id {
			t.Errorf("closing result subscription id = %s, want %s", got, id)
		}
	})
}
