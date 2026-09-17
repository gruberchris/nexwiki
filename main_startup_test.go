package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"nexwiki/server"
)

// runMainEnv, set to "1", makes this test binary run main() instead of the tests. The startup tests
// re-execute the binary with it, so they drive the real startup sequence, flag parsing and fatal
// exits included, in a child process.
const runMainEnv = "NEXWIKI_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// syncBuffer collects a child's output and is safe to read while the child is still writing it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// mainRun is a child process running main().
type mainRun struct {
	cmd            *exec.Cmd
	stdout, stderr syncBuffer
	done           chan struct{} // closed once the process has exited and its output is complete
	err            error         // what Wait returned; read only after done is closed
}

// startMain runs main() with args in a child process. The child gets no NEXWIKI_ variables, so the
// developer's own configuration cannot steer it, and its home and config directories are home, so a
// default data directory resolves under home instead of the real one. A nil stdin reads as EOF.
func startMain(t *testing.T, home string, stdin io.Reader, args ...string) *mainRun {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NEXWIKI_") {
			env = append(env, kv)
		}
	}
	config := filepath.Join(home, ".config")
	// exec uses the last value of a repeated key, so these replace any inherited ones.
	env = append(env, runMainEnv+"=1", "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+config, "APPDATA="+config)

	r := &mainRun{cmd: exec.Command(os.Args[0], args...), done: make(chan struct{})}
	r.cmd.Env = env
	r.cmd.Stdin = stdin
	r.cmd.Stdout = &r.stdout
	r.cmd.Stderr = &r.stderr
	if err := r.cmd.Start(); err != nil {
		t.Fatalf("starting main: %v", err)
	}
	go func() {
		defer close(r.done)
		r.err = r.cmd.Wait()
	}()
	t.Cleanup(func() {
		select {
		case <-r.done:
		default:
			_ = r.cmd.Process.Kill()
			<-r.done
		}
	})
	return r
}

// wait returns the child's exit code, failing the test if it has not exited within timeout.
func (r *mainRun) wait(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(timeout):
		_ = r.cmd.Process.Kill()
		<-r.done
		t.Fatalf("main did not exit within %s; stderr:\n%s", timeout, r.stderr.String())
	}
	var exitErr *exec.ExitError
	switch {
	case r.err == nil:
		return 0
	case errors.As(r.err, &exitErr):
		return exitErr.ExitCode()
	default:
		t.Fatalf("waiting for main: %v", r.err)
		return -1
	}
}

// waitForLog waits until the child has logged want, failing if it exits or a minute passes first.
func (r *mainRun) waitForLog(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !strings.Contains(r.stderr.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("main did not log %q within a minute; stderr:\n%s", want, r.stderr.String())
		}
		select {
		case <-r.done:
			t.Fatalf("main exited (%v) before logging %q; stderr:\n%s", r.err, want, r.stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// freePort returns a loopback port that nothing was listening on a moment ago.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists after the launch, or could not be checked (stat error: %v)", path, err)
	}
}

// assertNothingStarted checks the log for the lines the stdio loop and the plan lifecycle worker
// print as they start.
func assertNothingStarted(t *testing.T, stderr string) {
	t.Helper()
	for _, started := range []string{"stdio MCP server loop", "Plan lifecycle worker"} {
		if strings.Contains(stderr, started) {
			t.Errorf("the launch started a writer (%q) before failing; stderr:\n%s", started, stderr)
		}
	}
}

// TestBindFailureLeavesTheDataDirectoryUntouched pins #169: a normal launch on a port that is already
// taken, as when a stdio client config omits -mcp-only while a web server runs, exits with the bind
// error before creating, seeding, or locking any data directory, the default one included.
func TestBindFailureLeavesTheDataDirectoryUntouched(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	port := strconv.Itoa(occupied.Addr().(*net.TCPAddr).Port)

	for _, tc := range []struct {
		name         string
		explicitData bool
	}{
		{"explicit -data", true},
		{"default data directory", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			// Where the default resolves, given the config directory startMain sets.
			dataDir := filepath.Join(home, ".config", "nexwiki", "nexwiki-data")
			args := []string{"-bind", "127.0.0.1", "-port", port}
			if tc.explicitData {
				dataDir = filepath.Join(home, "wiki")
				args = append(args, "-data", dataDir)
			}

			run := startMain(t, home, nil, args...)
			if code := run.wait(t, time.Minute); code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			stderr := run.stderr.String()
			if want := "could not bind web server to 127.0.0.1:" + port; !strings.Contains(stderr, want) {
				t.Errorf("stderr does not report %q:\n%s", want, stderr)
			}
			assertNotExist(t, dataDir)
			assertNothingStarted(t, stderr)
		})
	}
}

// TestStorageFailureAfterBindReportsTheStorageError covers the other failure a normal launch can
// meet once the port is bound: storage failing to open is reported as itself, not as a bind error,
// and nothing starts.
func TestStorageFailureAfterBindReportsTheStorageError(t *testing.T) {
	home := t.TempDir()
	// A regular file where the data directory should be, so storage cannot create its directories.
	notADir := filepath.Join(home, "wiki")
	if err := os.WriteFile(notADir, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	run := startMain(t, home, nil, "-bind", "127.0.0.1", "-port", freePort(t), "-data", notADir)
	if code := run.wait(t, time.Minute); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	stderr := run.stderr.String()
	if !strings.Contains(stderr, "Fatal: failed to initialize storage") {
		t.Errorf("stderr does not report the storage failure:\n%s", stderr)
	}
	if strings.Contains(stderr, "could not bind") {
		t.Errorf("a storage failure was reported as a bind failure:\n%s", stderr)
	}
	assertNothingStarted(t, stderr)
}

// TestStandaloneMCPOnlySeedsTheAgentGuidelines pins #165: a -mcp-only process that finds no web
// server owns its data directory, so it seeds nexwiki-agent-guidelines before it answers the first
// request, as the web server does. The request is waiting on stdin before the process starts.
func TestStandaloneMCPOnlySeedsTheAgentGuidelines(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "wiki")
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_article","arguments":{"slug":"` +
		server.AgentGuidelinesSlug + `"}}}` + "\n"

	// Nothing answers on a free port, so the probe finds no primary and the process runs standalone.
	run := startMain(t, home, strings.NewReader(request), "-mcp-only", "-port", freePort(t), "-data", dataDir)
	if code := run.wait(t, time.Minute); code != 0 {
		t.Fatalf("exit code %d, want 0 at stdin EOF; stderr:\n%s", code, run.stderr.String())
	}
	if stderr := run.stderr.String(); !strings.Contains(stderr, "running standalone") {
		t.Fatalf("the process did not run standalone; stderr:\n%s", stderr)
	}

	lines := strings.Split(strings.TrimSpace(run.stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d lines, want the one response:\n%s", len(lines), run.stdout.String())
	}
	var resp struct {
		Result *struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("stdout is not a JSON-RPC response: %v\n%s", err, lines[0])
	}
	if resp.Result == nil || resp.Result.IsError || len(resp.Result.Content) == 0 {
		t.Fatalf("read_article(%s) failed, so the skill was not seeded before serving: %s", server.AgentGuidelinesSlug, lines[0])
	}
	if want := "Slug: " + server.AgentGuidelinesSlug; !strings.Contains(resp.Result.Content[0].Text, want) {
		t.Errorf("read_article returned no %q:\n%s", want, resp.Result.Content[0].Text)
	}
}

// TestProxyModeNeverOpensStorage pins that a -mcp-only process which finds a web server on its port
// only forwards to it: the data directory belongs to that server, so this process never creates it.
func TestProxyModeNeverOpensStorage(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer primary.Close()
	u, err := url.Parse(primary.URL)
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	dataDir := filepath.Join(home, "wiki")
	run := startMain(t, home, nil, "-mcp-only", "-port", u.Port(), "-data", dataDir)
	if code := run.wait(t, time.Minute); code != 0 {
		t.Errorf("exit code %d, want 0 at stdin EOF", code)
	}
	if stderr := run.stderr.String(); !strings.Contains(stderr, "running as a proxy") {
		t.Errorf("the process did not run as a proxy; stderr:\n%s", stderr)
	}
	assertNotExist(t, dataDir)
}

// TestNormalLaunchServesOnlyOnceSeededAndShutsDownOnSignal guards the bind moving ahead of storage:
// the listener is held while storage opens, but the first request answered already sees the seeded
// wiki, and SIGTERM still drains and closes everything.
func TestNormalLaunchServesOnlyOnceSeededAndShutsDownOnSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a child process cannot be sent SIGTERM on Windows")
	}
	home := t.TempDir()
	port := freePort(t)
	run := startMain(t, home, nil, "-bind", "127.0.0.1", "-port", port, "-data", filepath.Join(home, "wiki"))

	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	guidelines := "http://127.0.0.1:" + port + "/api/articles/" + server.AgentGuidelinesSlug
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := client.Get(guidelines)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the first request answered got %d, want 200: it was served before seeding", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the web server did not answer within a minute: %v; stderr:\n%s", err, run.stderr.String())
		}
		select {
		case <-run.done:
			t.Fatalf("main exited before serving (%v); stderr:\n%s", run.err, run.stderr.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
	client.CloseIdleConnections()

	if err := run.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}
	if code := run.wait(t, time.Minute); code != 0 {
		t.Errorf("exit code %d after SIGTERM, want 0", code)
	}
	if stderr := run.stderr.String(); !strings.Contains(stderr, "NexWiki shut down cleanly.") {
		t.Errorf("shutdown did not finish cleanly; stderr:\n%s", stderr)
	}
}

// TestSignalWhileStorageOpensIsHandledOnceItOpens pins that a process opening its data directory
// catches SIGTERM from before NewStorage, which cannot be interrupted: the signal does not kill it
// mid-open, and as soon as storage is open it closes it and exits 0, before seeding or starting
// anything. Another storage instance holds the index lock, so the signal lands during the lock wait.
func TestSignalWhileStorageOpensIsHandledOnceItOpens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a child process cannot be sent SIGTERM on Windows")
	}
	for _, tc := range []struct {
		name string
		args []string
		// ready is logged once the signal handler is registered, before storage starts opening.
		ready string
	}{
		{"web server", []string{"-bind", "127.0.0.1"}, "Serving frontend assets"},
		{"standalone -mcp-only", []string{"-mcp-only"}, "running standalone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dataDir := filepath.Join(home, "wiki")
			holder, err := server.NewStorage(dataDir)
			if err != nil {
				t.Fatalf("opening the storage that holds the lock: %v", err)
			}
			released := false
			t.Cleanup(func() {
				if !released {
					_ = holder.Close()
				}
			})

			// Stdin stays open, so a standalone -mcp-only process that got past startup would keep
			// serving instead of ending at EOF.
			stdin, stdinWriter, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdinWriter.Close() }()
			defer func() { _ = stdin.Close() }()

			args := append(append([]string{}, tc.args...), "-port", freePort(t), "-data", dataDir)
			run := startMain(t, home, stdin, args...)
			run.waitForLog(t, tc.ready)
			if err := run.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("sending SIGTERM: %v", err)
			}
			select {
			case <-run.done:
				t.Fatalf("SIGTERM ended main while it waited for the index lock (%v); stderr:\n%s", run.err, run.stderr.String())
			case <-time.After(500 * time.Millisecond):
			}

			released = true
			if err := holder.Close(); err != nil {
				t.Fatalf("releasing the index lock: %v", err)
			}
			if code := run.wait(t, 10*time.Second); code != 0 {
				t.Fatalf("exit code %d, want 0; stderr:\n%s", code, run.stderr.String())
			}
			stderr := run.stderr.String()
			for _, want := range []string{"Received terminated while opening storage", "NexWiki shut down cleanly."} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr does not say %q:\n%s", want, stderr)
				}
			}
			for _, started := range []string{"Seeded default governance skill", "stdio MCP server loop", "Plan lifecycle worker", "web server is running"} {
				if strings.Contains(stderr, started) {
					t.Errorf("main went on past opening storage (%q) despite the signal; stderr:\n%s", started, stderr)
				}
			}
			assertNotExist(t, filepath.Join(dataDir, "articles", server.AgentGuidelinesSlug+".md"))

			reopened, err := server.NewStorage(dataDir)
			if err != nil {
				t.Fatalf("the index did not reopen after main closed it: %v", err)
			}
			if err := reopened.Close(); err != nil {
				t.Errorf("closing the reopened storage: %v", err)
			}
		})
	}
}
