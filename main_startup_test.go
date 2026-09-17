package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"nexwiki/server"
)

// runMainEnv, set to "1", makes this test binary run main() instead of the tests. The startup tests
// re-execute the binary with it, so they drive the real startup sequence, flag parsing and fatal
// exits included, in a child process.
const runMainEnv = "NEXWIKI_TEST_RUN_MAIN"

// pauseBeforeStorageEnv, set to "1" as well, holds the child just before it checks for a signal and
// opens storage, logging pausedBeforeStorage, until a signal is waiting for that check. A signal sent
// once the line is logged is certain to arrive before NewStorage starts.
const (
	pauseBeforeStorageEnv = "NEXWIKI_TEST_PAUSE_BEFORE_STORAGE"
	pausedBeforeStorage   = "test hook: paused before opening storage"
)

// waitForServerEnv, set to "host:port", makes this test binary run only waitForServer against that
// address, exiting 0 if the server answered within waitForServerTimeout and 1 otherwise. It takes
// precedence over runMainEnv.
const (
	waitForServerEnv     = "NEXWIKI_TEST_WAIT_FOR_SERVER"
	waitForServerTimeout = 5 * time.Second
)

func TestMain(m *testing.M) {
	if addr := os.Getenv(waitForServerEnv); addr != "" {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || !waitForServer(host, port, waitForServerTimeout) {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv(runMainEnv) == "1" {
		if os.Getenv(pauseBeforeStorageEnv) == "1" {
			beforeOpenStorage = holdUntilSignalled
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// holdUntilSignalled returns once a signal is waiting in sigCh. It takes the signal to see it arrive
// and puts it back for main's own check, unless another signal already refilled the buffer.
func holdUntilSignalled(sigCh chan os.Signal) {
	log.Print(pausedBeforeStorage)
	sig := <-sigCh
	select {
	case sigCh <- sig:
	default:
	}
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
	return startMainWithEnv(t, home, stdin, nil, args...)
}

// startMainWithEnv is startMain with extraEnv ("KEY=value") added to the child's environment, after
// the NEXWIKI_ variables are removed, so it can set the test hooks.
func startMainWithEnv(t *testing.T, home string, stdin io.Reader, extraEnv []string, args ...string) *mainRun {
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
	env = append(env, extraEnv...)

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

// waitForLog waits until the child has logged want, failing if it exits or timeout passes first.
func (r *mainRun) waitForLog(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(r.stderr.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("main did not log %q within %s; stderr:\n%s", want, timeout, r.stderr.String())
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
// It finds the server on loopback from the native and container defaults, and at a specific address
// given as its own -bind (#174), and forwards the stdio request to the host it found.
func TestProxyModeNeverOpensStorage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		primaryHost string
		args, env   []string
	}{
		{"native default", "127.0.0.1", nil, nil},
		{"container NEXWIKI_BIND=0.0.0.0", "127.0.0.1", nil, []string{"NEXWIKI_BIND=0.0.0.0"}},
		{"-bind ::1", "::1", []string{"-bind", "::1"}, nil},
		{"-bind 127.0.0.2", "127.0.0.2", []string{"-bind", "127.0.0.2"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, mcpCalls := startPrimaryStub(t, tc.primaryHost)
			request := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"

			home := t.TempDir()
			dataDir := filepath.Join(home, "wiki")
			args := append(append([]string{"-mcp-only"}, tc.args...), "-port", port, "-data", dataDir)
			run := startMainWithEnv(t, home, strings.NewReader(request), tc.env, args...)
			if code := run.wait(t, time.Minute); code != 0 {
				t.Errorf("exit code %d, want 0 at stdin EOF; stderr:\n%s", code, run.stderr.String())
			}
			if stderr := run.stderr.String(); !strings.Contains(stderr, "running as a proxy") {
				t.Fatalf("the process did not run as a proxy; stderr:\n%s", stderr)
			}
			if got := mcpCalls.Load(); got != 1 {
				t.Errorf("the primary on %s received %d MCP requests, want 1", tc.primaryHost, got)
			}
			if want := `"answeredOn":"` + tc.primaryHost + `"`; !strings.Contains(run.stdout.String(), want) {
				t.Errorf("stdout does not carry the primary's answer %s:\n%s", want, run.stdout.String())
			}
			assertNotExist(t, dataDir)
		})
	}
}

// nonLoopbackAddress returns an address of this machine outside loopback that a test server can
// listen on, IPv4 preferred, skipping the test when there is none. The default transport exempts all
// of loopback, 127.0.0.2 included, from the proxy variables, so only such an address shows whether a
// client honors them.
func nonLoopbackAddress(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("cannot list interface addresses: %v", err)
	}
	var v4, v6 []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		// Global unicast includes private LAN ranges, and leaves out loopback and link-local, whose
		// IPv6 form would need a zone.
		if !ok || !ipnet.IP.IsGlobalUnicast() {
			continue
		}
		if ipnet.IP.To4() != nil {
			v4 = append(v4, ipnet.IP.String())
		} else {
			v6 = append(v6, ipnet.IP.String())
		}
	}
	for _, host := range append(v4, v6...) {
		if ln, err := net.Listen("tcp", net.JoinHostPort(host, "0")); err == nil {
			_ = ln.Close()
			return host
		}
	}
	t.Skip("no non-loopback address to listen on")
	return ""
}

// TestProxyModeConnectsDirectlyDespiteProxyVariables pins that a sidecar's traffic to its primary
// never goes through HTTP_PROXY: a primary bound to a non-loopback address, which the default
// transport would reach through the proxy, is found and forwarded to directly, and the recording
// proxy receives nothing. The variables go to a child process because net/http reads them once per
// process, so setting them in this one could come too late to have any effect.
func TestProxyModeConnectsDirectlyDespiteProxyVariables(t *testing.T) {
	host := nonLoopbackAddress(t)
	port, mcpCalls := startPrimaryStub(t, host)

	var proxied atomic.Int32
	recordingProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		http.Error(w, "the recording proxy forwards nothing", http.StatusBadGateway)
	}))
	defer recordingProxy.Close()
	env := proxyVariables(recordingProxy.URL)

	home := t.TempDir()
	dataDir := filepath.Join(home, "wiki")
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	run := startMainWithEnv(t, home, strings.NewReader(request), env, "-mcp-only", "-bind", host, "-port", port, "-data", dataDir)
	if code := run.wait(t, time.Minute); code != 0 {
		t.Errorf("exit code %d, want 0 at stdin EOF; stderr:\n%s", code, run.stderr.String())
	}
	if stderr := run.stderr.String(); !strings.Contains(stderr, "running as a proxy") {
		t.Fatalf("the primary on %s was not detected (the proxy received %d requests); stderr:\n%s", host, proxied.Load(), stderr)
	}
	if got := mcpCalls.Load(); got != 1 {
		t.Errorf("the primary on %s received %d MCP requests, want 1", host, got)
	}
	if want := `"answeredOn":"` + host + `"`; !strings.Contains(run.stdout.String(), want) {
		t.Errorf("stdout does not carry the primary's answer %s:\n%s", want, run.stdout.String())
	}
	if got := proxied.Load(); got != 0 {
		t.Errorf("%d requests went through HTTP_PROXY, want none", got)
	}
	assertNotExist(t, dataDir)
}

// proxyVariables points every proxy environment variable at proxyURL, and empties NO_PROXY so the
// developer's own exemptions cannot make a test pass.
func proxyVariables(proxyURL string) []string {
	env := []string{"NO_PROXY=", "no_proxy="}
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		env = append(env, key+"="+proxyURL)
	}
	return env
}

// TestWaitForServerConnectsDirectlyDespiteProxyVariables pins that the -launch-in-browser
// readiness poll reaches a server bound to a non-loopback address directly, not through
// HTTP_PROXY, which would fail it until its timeout. It runs in a child process for the same reason
// as TestProxyModeConnectsDirectlyDespiteProxyVariables.
func TestWaitForServerConnectsDirectlyDespiteProxyVariables(t *testing.T) {
	host := nonLoopbackAddress(t)
	port, _ := startPrimaryStub(t, host)

	var proxied atomic.Int32
	recordingProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		http.Error(w, "the recording proxy forwards nothing", http.StatusBadGateway)
	}))
	defer recordingProxy.Close()

	env := append(proxyVariables(recordingProxy.URL), waitForServerEnv+"="+net.JoinHostPort(host, port))
	run := startMainWithEnv(t, t.TempDir(), nil, env)
	if code := run.wait(t, waitForServerTimeout+time.Minute); code != 0 {
		t.Errorf("waitForServer did not reach the server on %s (exit code %d; the proxy received %d requests)", host, code, proxied.Load())
	}
	if got := proxied.Load(); got != 0 {
		t.Errorf("%d requests went through HTTP_PROXY, want none", got)
	}
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
// catches SIGTERM from before NewStorage, which cannot be interrupted: the signal is acknowledged at
// once, neither it nor a second one kills the process mid-open, and as soon as storage is open it
// closes it and exits 0, before seeding or starting anything. Another storage instance holds the
// index lock, so the signals land during the lock wait.
func TestSignalWhileStorageOpensIsHandledOnceItOpens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a child process cannot be sent SIGTERM on Windows")
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"web server", []string{"-bind", "127.0.0.1"}},
		{"standalone -mcp-only", []string{"-mcp-only"}},
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
			// Logged after the check for a signal that arrived earlier and just before NewStorage
			// starts, so a signal sent from here lands during the open.
			run.waitForLog(t, "Opening storage...", time.Minute)
			if err := run.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("sending SIGTERM: %v", err)
			}
			// Well inside IndexOpenTimeout, so the acknowledgement comes while the lock is still held,
			// not once NewStorage gives up or returns.
			run.waitForLog(t, "Received terminated while opening storage; will shut down as soon as it finishes opening", 5*time.Second)

			if err := run.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("sending the second SIGTERM: %v", err)
			}
			run.waitForLog(t, "Received terminated while opening storage; shutdown is already pending", 5*time.Second)
			select {
			case <-run.done:
				t.Fatalf("a SIGTERM ended main while it waited for the index lock (%v); stderr:\n%s", run.err, run.stderr.String())
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
			for _, want := range []string{"Storage finished opening", "NexWiki shut down cleanly."} {
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

// TestSignalBeforeStorageOpensLeavesTheDataDirectoryUncreated pins that a signal arriving before
// NewStorage starts, while the frontend loads or the port is bound, ends the launch there: it exits
// 0 without creating the data directory or starting anything. A test hook holds startup until the
// signal is waiting, so it certainly arrives before the open.
func TestSignalBeforeStorageOpensLeavesTheDataDirectoryUncreated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a child process cannot be sent SIGTERM on Windows")
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"web server", []string{"-bind", "127.0.0.1"}},
		{"standalone -mcp-only", []string{"-mcp-only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dataDir := filepath.Join(home, "wiki")
			args := append(append([]string{}, tc.args...), "-port", freePort(t), "-data", dataDir)
			run := startMainWithEnv(t, home, nil, []string{pauseBeforeStorageEnv + "=1"}, args...)
			run.waitForLog(t, pausedBeforeStorage, time.Minute)
			if err := run.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("sending SIGTERM: %v", err)
			}
			if code := run.wait(t, 10*time.Second); code != 0 {
				t.Fatalf("exit code %d, want 0; stderr:\n%s", code, run.stderr.String())
			}
			stderr := run.stderr.String()
			if want := "Received terminated before opening storage"; !strings.Contains(stderr, want) {
				t.Errorf("stderr does not say %q:\n%s", want, stderr)
			}
			if strings.Contains(stderr, "Opening storage") {
				t.Errorf("main went on to open storage despite the signal; stderr:\n%s", stderr)
			}
			assertNotExist(t, dataDir)
			assertNothingStarted(t, stderr)
		})
	}
}

// TestNormalLaunchBindsIPv6Loopback pins that -bind takes an IPv6 literal. The listen address was
// once formatted as "::1:port", which net.Listen rejects with "too many colons"; now the server
// answers on [::1], and the banner prints a URL with the host bracketed.
func TestNormalLaunchBindsIPv6Loopback(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()

	home := t.TempDir()
	run := startMain(t, home, nil, "-bind", "::1", "-port", port, "-data", filepath.Join(home, "wiki"))
	run.waitForLog(t, "NexWiki web server is running on http://[::1]:"+port+"\n", time.Minute)

	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := client.Get("http://[::1]:" + port + "/api/config")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /api/config over [::1] got %d, want 200", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the web server did not answer on [::1] within a minute: %v; stderr:\n%s", err, run.stderr.String())
		}
		select {
		case <-run.done:
			t.Fatalf("main exited before serving (%v); stderr:\n%s", run.err, run.stderr.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
	client.CloseIdleConnections()

	if runtime.GOOS == "windows" {
		return // a child process cannot be sent SIGTERM on Windows; the cleanup kills it
	}
	if err := run.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}
	if code := run.wait(t, time.Minute); code != 0 {
		t.Errorf("exit code %d after SIGTERM, want 0; stderr:\n%s", code, run.stderr.String())
	}
}
