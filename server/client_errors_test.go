package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// TestDataDirHiderHide pins how an error reaching a client names paths: relative to the data
// directory, whichever subdirectory or file under it the error is about, and nothing else touched.
func TestDataDirHiderHide(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "data")
	sep := string(filepath.Separator)
	j := filepath.Join

	for _, tc := range []struct {
		name, err, want string
	}{
		{
			name: "an article subdirectory",
			err:  "failed to list articles: open " + j(abs, "articles", "sub") + ": permission denied",
			want: "failed to list articles: open " + j("articles", "sub") + ": permission denied",
		},
		{
			name: "the article directory",
			err:  "open " + j(abs, "articles") + ": permission denied",
			want: "open articles: permission denied",
		},
		{
			name: "a history snapshot",
			err:  "open " + j(abs, "history", "foo", "3.md.gz") + ": no such file or directory",
			want: "open " + j("history", "foo", "3.md.gz") + ": no such file or directory",
		},
		{
			name: "an asset",
			err:  "failed to write asset file: open " + j(abs, "assets", "foo", "diagram.png") + ": read-only file system",
			want: "failed to write asset file: open " + j("assets", "foo", "diagram.png") + ": read-only file system",
		},
		{
			name: "the activity log",
			err:  "open " + j(abs, ActivityLogFilename) + ": permission denied",
			want: "open " + ActivityLogFilename + ": permission denied",
		},
		{
			name: "an activity log archive",
			err:  "open " + j(abs, "activity-2026-01-02T03-04-05Z.jsonl") + ": input/output error",
			want: "open activity-2026-01-02T03-04-05Z.jsonl: input/output error",
		},
		{
			name: "an OKF export",
			err:  "open " + j(abs, "okf-export-2026-09-16T10-00-00Z.zip") + ": disk quota exceeded",
			want: "open okf-export-2026-09-16T10-00-00Z.zip: disk quota exceeded",
		},
		{
			name: "two paths in one error",
			err:  "rename " + j(abs, "custom_themes.json.tmp") + " " + j(abs, "custom_themes.json") + ": permission denied",
			want: "rename custom_themes.json.tmp custom_themes.json: permission denied",
		},
		{
			name: "the search index",
			err:  "open " + j(abs, "search.bleve", "store") + ": resource temporarily unavailable",
			want: "open " + j("search.bleve", "store") + ": resource temporarily unavailable",
		},
		{
			name: "a quoted path",
			err:  `stat "` + j(abs, "articles", "a.md") + `": permission denied`,
			want: `stat "` + j("articles", "a.md") + `": permission denied`,
		},
		{
			name: "the data directory itself",
			err:  "lstat " + abs + ": permission denied",
			want: "lstat .: permission denied",
		},
		{
			name: "the data directory at the end of the text",
			err:  "cannot walk " + abs,
			want: "cannot walk .",
		},
		{
			name: "the data directory in quotes",
			err:  `cannot walk "` + abs + `"`,
			want: `cannot walk "."`,
		},
		{
			name: "a sibling directory whose name starts with the data directory's is left alone",
			err:  "open " + abs + "-old: permission denied",
			want: "open " + abs + "-old: permission denied",
		},
		{
			name: "a file in a sibling directory is left alone",
			err:  "open " + j(abs+"-old", "articles", "x.md") + ": permission denied",
			want: "open " + j(abs+"-old", "articles", "x.md") + ": permission denied",
		},
		{
			name: "a sibling at the end of the text is left alone",
			err:  "cannot walk " + abs + ".bak",
			want: "cannot walk " + abs + ".bak",
		},
		{
			name: "a longer path that merely contains the data directory's is left alone",
			err:  "open " + sep + "mnt" + j(abs, "articles", "x.md") + ": permission denied",
			want: "open " + sep + "mnt" + j(abs, "articles", "x.md") + ": permission denied",
		},
		{
			name: "an error naming no path is left alone",
			err:  "invalid format: missing front matter header marker",
			want: "invalid format: missing front matter header marker",
		},
		{
			name: "empty text",
			err:  "",
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newDataDirHider(abs).hide(tc.err); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	// A -data given relative to the working directory yields relative paths from Join, and
	// absolute ones wherever a caller made them absolute. Both come out relative to the data
	// directory, however -data was spelled.
	t.Run("a relative data directory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		rel := j("srv", "data")
		absRel, err := filepath.Abs(rel)
		if err != nil {
			t.Fatalf("Abs failed: %v", err)
		}
		for _, dataDir := range []string{rel, "." + sep + rel, rel + sep} {
			hider := newDataDirHider(dataDir)
			for _, tc := range []struct{ err, want string }{
				{
					err:  "open " + j(rel, "articles", "sub") + ": permission denied",
					want: "open " + j("articles", "sub") + ": permission denied",
				},
				{
					err:  "open " + j(absRel, "history", "x", "1.md.gz") + ": permission denied",
					want: "open " + j("history", "x", "1.md.gz") + ": permission denied",
				},
				{
					err:  "open " + absRel + ": permission denied",
					want: "open .: permission denied",
				},
				// With a separator in it the bare relative form is recognizably a path, and it is
				// the data directory as much as the absolute form is.
				{
					err:  "open " + rel + ": permission denied",
					want: "open .: permission denied",
				},
				{
					err:  "open " + rel + "-old: permission denied",
					want: "open " + rel + "-old: permission denied",
				},
				// Nor is the relative form matched partway through some other path.
				{
					err:  "open " + j(sep+"elsewhere", rel, "articles", "x.md") + ": permission denied",
					want: "open " + j(sep+"elsewhere", rel, "articles", "x.md") + ": permission denied",
				},
			} {
				if got := hider.hide(tc.err); got != tc.want {
					t.Errorf("-data %q: got %q, want %q", dataDir, got, tc.want)
				}
			}
		}
	})

	// Hiding the bare relative directory turns on whether it is recognizably a path: one with a
	// separator is, while a single name such as "data" could as well be an ordinary word, and
	// naming it reveals nothing about where the server keeps it.
	t.Run("a bare relative data directory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		nested := j("..", "..", "srv", "wiki")
		for _, tc := range []struct {
			name, dataDir, err, want string
		}{
			{
				name:    "with a separator, the directory itself",
				dataDir: nested,
				err:     "open " + nested + ": permission denied",
				want:    "open .: permission denied",
			},
			{
				name:    "with a separator, at the end of the text",
				dataDir: nested,
				err:     "cannot walk " + nested,
				want:    "cannot walk .",
			},
			{
				name:    "with a separator, in quotes",
				dataDir: nested,
				err:     `cannot walk "` + nested + `"`,
				want:    `cannot walk "."`,
			},
			{
				name:    "with a separator, a path under it",
				dataDir: nested,
				err:     "open " + j(nested, "articles", "x.md") + ": permission denied",
				want:    "open " + j("articles", "x.md") + ": permission denied",
			},
			{
				name:    "with a separator, a sibling is left alone",
				dataDir: nested,
				err:     "open " + nested + "-old: permission denied",
				want:    "open " + nested + "-old: permission denied",
			},
			{
				// Concatenated, since Join would clean the .. away.
				name:    "with a separator, inside a longer path is left alone",
				dataDir: nested,
				err:     "open " + sep + "mnt" + sep + nested + ": permission denied",
				want:    "open " + sep + "mnt" + sep + nested + ": permission denied",
			},
			{
				name:    "a single name is left alone",
				dataDir: "data",
				err:     "open data: permission denied",
				want:    "open data: permission denied",
			},
			{
				name:    "a single name as a word is left alone",
				dataDir: "data",
				err:     "yaml: unmarshal errors: cannot unmarshal data into string",
				want:    "yaml: unmarshal errors: cannot unmarshal data into string",
			},
			{
				name:    "a single name, a path under it",
				dataDir: "data",
				err:     "open " + j("data", "articles") + ": permission denied",
				want:    "open articles: permission denied",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := newDataDirHider(tc.dataDir).hide(tc.err); got != tc.want {
					t.Errorf("-data %q: got %q, want %q", tc.dataDir, got, tc.want)
				}
			})
		}
	})

	// A path resolved through a symlinked data directory names where the link points.
	t.Run("a symlinked data directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating symlinks needs privileges on Windows")
		}
		target := j(t.TempDir(), "target")
		if err := os.MkdirAll(target, 0755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}
		// The temp directory may itself sit behind a symlink (/var on macOS).
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("EvalSymlinks failed: %v", err)
		}
		link := j(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("Symlink failed: %v", err)
		}
		hider := newDataDirHider(link)
		for _, tc := range []struct{ err, want string }{
			{err: "open " + j(link, "articles") + ": permission denied", want: "open articles: permission denied"},
			{err: "open " + j(resolved, "articles", "x.md") + ": permission denied", want: "open " + j("articles", "x.md") + ": permission denied"},
		} {
			if got := hider.hide(tc.err); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		}
	})

	// Where one form of the data directory starts another, both can match at one position, and only
	// the longer leaves a path relative to the data directory. The resolved form can start the
	// absolute one, for a data directory that links to the directory it sits in, and the absolute
	// form can start the resolved one wherever the link target's name runs on past the link's in a
	// character that can also end a path.
	t.Run("forms that overlap", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating symlinks needs privileges on Windows")
		}
		for _, tc := range []struct {
			name string
			// target is the directory the data directory links to, relative to the one both sit
			// in; "." is that directory itself.
			target string
		}{
			{name: "a link to the directory it sits in", target: "."},
			{name: "a link to a sibling named on past a space", target: "wiki 2"},
			{name: "a link to a sibling named on past a comma", target: "wiki,v2"},
			{name: "a link to a sibling named on past a colon", target: "wiki:real"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// Resolved first, so the temp directory's own links (/var on macOS) cannot keep
				// one form from starting the other.
				base, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatalf("EvalSymlinks failed: %v", err)
				}
				target := j(base, tc.target)
				if err := os.MkdirAll(target, 0755); err != nil {
					t.Skipf("this filesystem does not allow a directory named %q: %v", tc.target, err)
				}
				link := j(base, "wiki")
				if err := os.Symlink(target, link); err != nil {
					t.Fatalf("Symlink failed: %v", err)
				}
				resolved, err := filepath.EvalSymlinks(link)
				if err != nil || resolved != target {
					t.Fatalf("EvalSymlinks(%q) = %q, %v; want %q", link, resolved, err, target)
				}

				hider := newDataDirHider(link)
				for _, c := range []struct{ err, want string }{
					{err: "open " + j(link, "articles", "x.md") + ": permission denied", want: "open " + j("articles", "x.md") + ": permission denied"},
					{err: "lstat " + link + ": permission denied", want: "lstat .: permission denied"},
					{err: "open " + j(resolved, "articles", "x.md") + ": permission denied", want: "open " + j("articles", "x.md") + ": permission denied"},
					{err: "lstat " + resolved + ": permission denied", want: "lstat .: permission denied"},
					{err: "rename " + j(resolved, "a.tmp") + " " + j(resolved, "a") + ": permission denied", want: "rename a.tmp a: permission denied"},
				} {
					if got := hider.hide(c.err); got != c.want {
						t.Errorf("got %q, want %q", got, c.want)
					}
				}
			})
		}
	})

	// A data directory at the filesystem root contains every path, so there is nothing it could
	// hide without mangling paths that are not the wiki's.
	t.Run("a root data directory", func(t *testing.T) {
		text := "open " + j(sep, "etc", "passwd") + ": permission denied; a " + sep + " b"
		if got := newDataDirHider(sep).hide(text); got != text {
			t.Errorf("got %q, want the text unchanged", got)
		}
	})
}

// TestDataDirHiderHideReturnsUnmatchedTextUncopied pins that text in which nothing is replaced,
// which is most error text, comes back as the very string passed in: whether it names no form of
// the data directory at all, or names one only where no path starts or ends.
func TestDataDirHiderHideReturnsUnmatchedTextUncopied(t *testing.T) {
	t.Chdir(t.TempDir())
	abs := filepath.Join(t.TempDir(), "data")
	for _, tc := range []struct{ dataDir, text string }{
		{dataDir: abs, text: "invalid format: missing front matter header marker"},
		{dataDir: abs, text: "open " + filepath.Join(t.TempDir(), "articles") + ": permission denied"},
		{dataDir: abs, text: "open " + abs + "-old: permission denied"},
		{dataDir: abs, text: "open " + string(filepath.Separator) + "mnt" + filepath.Join(abs, "articles") + ": permission denied"},
		{dataDir: "data", text: "yaml: cannot unmarshal !!str `" + filepath.Join("metadata", "y") + "` into []string"},
	} {
		got := newDataDirHider(tc.dataDir).hide(tc.text)
		if got != tc.text || unsafe.StringData(got) != unsafe.StringData(tc.text) {
			t.Errorf("-data %q: hide(%q) returned a copy, %q", tc.dataDir, tc.text, got)
		}
	}
}

// lockListing leaves a directory searchable but not listable for the rest of the test: execute
// permission without read. A file in it still opens by name, so a request that loads one article
// before scanning the wiki gets as far as the scan, which then fails listing the directory. The
// test is skipped where that is not what happens.
func lockListing(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.Chmod(dir, 0300); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("removing read permission does not stop listing a directory here (running as root?)")
	}
	if _, err := os.ReadFile(filepath.Join(dir, file)); err != nil {
		t.Skipf("a directory without read permission cannot be searched here: %v", err)
	}
}

// assertHidesDataDir fails the test unless text, the error a client received, names the data
// directory by the relative path want and does not reveal where the data directory is.
func assertHidesDataDir(t *testing.T, srv *Server, label, text, want string) {
	t.Helper()
	abs, err := filepath.Abs(srv.Storage.DataDir)
	if err != nil {
		t.Fatalf("Abs failed: %v", err)
	}
	if strings.Contains(text, abs) {
		t.Errorf("%s reveals the server's data directory %q: %q", label, abs, text)
	}
	if !filepath.IsAbs(srv.Storage.DataDir) && strings.Contains(text, filepath.Clean(srv.Storage.DataDir)+string(filepath.Separator)) {
		t.Errorf("%s names paths by the configured data directory %q: %q", label, srv.Storage.DataDir, text)
	}
	if !strings.Contains(text, want) {
		t.Errorf("%s does not say %q: %q", label, want, text)
	}
}

// restError runs a REST handler and returns the error message of its JSON error response.
func restError(t *testing.T, handler http.HandlerFunc, method, target string, pathValues map[string]string, wantStatus int) string {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != wantStatus {
		t.Errorf("%s %s: status %d, want %d: %s", method, target, w.Code, wantStatus, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: response is not a JSON error: %v\n%s", method, target, err, w.Body.String())
	}
	return body["error"]
}

// TestUnlistableArticleDirErrorsHideDataDir is the case #158 reported: when the article directory
// cannot be listed, every MCP tool, resource listing, and REST endpoint that scans the wiki fails,
// and the OS error each passes on names the directory. None may say where it is on the server.
func TestUnlistableArticleDirErrorsHideDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes do not stop listing on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root lists directories whatever their mode")
	}

	for _, variant := range []struct {
		name   string
		newSrv func(*testing.T) *Server
	}{
		{name: "absolute data directory", newSrv: newMCPServer},
		{name: "relative data directory", newSrv: func(t *testing.T) *Server {
			t.Chdir(t.TempDir())
			storage, err := NewStorage("data")
			if err != nil {
				t.Fatalf("NewStorage failed: %v", err)
			}
			t.Cleanup(func() { _ = storage.Close() })
			return NewServer(storage, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
		}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			srv := variant.newSrv(t)
			captureLog(t)
			lockListing(t, srv.Storage.ArticleDir, "home.md")

			const want = "open articles: permission denied"

			for _, call := range []string{
				`{"name":"search_wiki","arguments":{}}`,
				// delete_article reports a failed inbound-link scan in its refusal.
				`{"name":"delete_article","arguments":{"slug":"home"}}`,
				`{"name":"get_wiki_overview","arguments":{}}`,
				`{"name":"wiki_health","arguments":{}}`,
			} {
				resp := toolCall(t, srv, call)
				if !resp.IsError || len(resp.Content) != 1 {
					t.Errorf("%s: expected one error block, got %+v", call, resp)
					continue
				}
				assertHidesDataDir(t, srv, call, resp.Content[0].Text, want)
			}
			// The refusal above is a guard, not just an error message: nothing may be deleted when
			// the scan could not run.
			if _, err := srv.Storage.GetArticle("home"); err != nil {
				t.Errorf("delete_article refused on a failed scan but home is gone: %v", err)
			}

			envelope := callJSON(t, srv, "resources/list", "")
			rpcErr, ok := envelope["error"].(map[string]interface{})
			if !ok {
				t.Fatalf("resources/list: expected a JSON-RPC error, got %v", envelope)
			}
			message, _ := rpcErr["message"].(string)
			assertHidesDataDir(t, srv, "resources/list", message, want)
			if n := strings.Count(message, "failed to list articles"); n != 1 {
				t.Errorf("resources/list says it failed to list articles %d times, want once: %q", n, message)
			}

			for _, tc := range []struct {
				handler    http.HandlerFunc
				target     string
				pathValues map[string]string
			}{
				{handler: srv.HandleListArticles, target: "/api/articles"},
				{handler: srv.HandleGetBacklinks, target: "/api/articles/home/backlinks", pathValues: map[string]string{"slug": "home"}},
				{handler: srv.HandleGetWikiStats, target: "/api/wiki/stats"},
				{handler: srv.HandleListSkills, target: "/api/skills"},
			} {
				msg := restError(t, tc.handler, http.MethodGet, tc.target, tc.pathValues, http.StatusInternalServerError)
				assertHidesDataDir(t, srv, "GET "+tc.target, msg, want)
			}
		})
	}
}

// TestUnreadableFileErrorsHideDataDir covers errors about one file rather than a whole scan: an
// article that cannot be read, and an activity log that cannot be, which sits at the top of the
// data directory rather than under articles/.
func TestUnreadableFileErrorsHideDataDir(t *testing.T) {
	toolsByName["get_recent_activity"] = &getRecentActivityTool
	t.Cleanup(func() { delete(toolsByName, "get_recent_activity") })

	if runtime.GOOS == "windows" {
		t.Skip("mode 0 does not stop reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	srv := newMCPServer(t)
	captureLog(t)

	lock := func(path string, content string) {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("Chmod failed: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0644) })
	}
	lock(filepath.Join(srv.Storage.ArticleDir, "locked.md"), "---\ntitle: Locked\nslug: locked\n---\nbody\n")
	lock(ActivityLogPath(srv.Storage.DataDir), "{}\n")

	const wantArticle = "open articles/locked.md: permission denied"
	resp := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"locked"}}`)
	if !resp.IsError || len(resp.Content) != 1 {
		t.Fatalf("read_article: expected one error block, got %+v", resp)
	}
	assertHidesDataDir(t, srv, "read_article", resp.Content[0].Text, wantArticle)
	msg := restError(t, srv.HandleGetArticle, http.MethodGet, "/api/articles/locked", map[string]string{"slug": "locked"}, http.StatusNotFound)
	assertHidesDataDir(t, srv, "GET /api/articles/locked", msg, wantArticle)

	const wantLog = "open " + ActivityLogFilename + ": permission denied"
	resp = toolCall(t, srv, `{"name":"get_recent_activity","arguments":{}}`)
	if !resp.IsError || len(resp.Content) != 1 {
		t.Fatalf("get_recent_activity: expected one error block, got %+v", resp)
	}
	assertHidesDataDir(t, srv, "get_recent_activity", resp.Content[0].Text, wantLog)
	msg = restError(t, srv.HandleGetActivityLog, http.MethodGet, "/api/activity/log", nil, http.StatusInternalServerError)
	assertHidesDataDir(t, srv, "GET /api/activity/log", msg, wantLog)
}

// TestImportOKFBundleReadErrorHidesDataDir covers an OKF path, which import resolves against the
// data directory before reading it, so the OS error names the absolute path.
func TestImportOKFBundleReadErrorHidesDataDir(t *testing.T) {
	toolsByName["import_okf_bundle"] = &importOkfBundleTool
	t.Cleanup(func() { delete(toolsByName, "import_okf_bundle") })

	srv := newMCPServer(t)

	resp := toolCall(t, srv, `{"name":"import_okf_bundle","arguments":{"path":"missing.zip"}}`)
	if !resp.IsError || len(resp.Content) != 1 {
		t.Fatalf("import_okf_bundle: expected one error block, got %+v", resp)
	}
	assertHidesDataDir(t, srv, "import_okf_bundle", resp.Content[0].Text, "Error reading bundle at 'missing.zip': open missing.zip:")
}

// importWarnings imports bundle every way a client can, through Storage, the import_okf_bundle MCP
// tool, and the REST endpoint, and returns the warnings each reports, keyed by which it was.
func importWarnings(t *testing.T, srv *Server, bundle []byte) map[string][]string {
	t.Helper()
	toolsByName["import_okf_bundle"] = &importOkfBundleTool
	t.Cleanup(func() { delete(toolsByName, "import_okf_bundle") })

	got := make(map[string][]string)

	report, err := srv.Storage.ImportOKFBundle(bundle)
	if err != nil {
		t.Fatalf("ImportOKFBundle failed: %v", err)
	}
	got["ImportOKFBundle"] = report.Warnings

	if err := os.WriteFile(filepath.Join(srv.Storage.DataDir, "bundle.zip"), bundle, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	resp := toolCall(t, srv, `{"name":"import_okf_bundle","arguments":{"path":"bundle.zip"}}`)
	if resp.IsError || len(resp.Content) != 1 {
		t.Fatalf("import_okf_bundle: expected one text block, got %+v", resp)
	}
	for _, line := range strings.Split(resp.Content[0].Text, "\n") {
		if warning, ok := strings.CutPrefix(line, "Warning: "); ok {
			got["import_okf_bundle"] = append(got["import_okf_bundle"], warning)
		}
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "bundle.zip")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := fw.Write(bundle); err != nil {
		t.Fatalf("writing the form file failed: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing the form failed: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/okf/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	srv.HandleImportOKFBundle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/okf/import: status %d: %s", w.Code, w.Body.String())
	}
	var restReport OKFImportReport
	if err := json.Unmarshal(w.Body.Bytes(), &restReport); err != nil {
		t.Fatalf("POST /api/okf/import: response is not a report: %v\n%s", err, w.Body.String())
	}
	got["POST /api/okf/import"] = restReport.Warnings
	return got
}

// newRelativeDataDirServer returns a server on a data directory given relative to a fresh working
// directory, as -data dataDir would be.
func newRelativeDataDirServer(t *testing.T, dataDir string) *Server {
	t.Helper()
	t.Chdir(t.TempDir())
	storage, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return NewServer(storage, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
}

// TestOKFImportWarningsKeepEntryNames pins that an import warning names the bundle entry exactly as
// the bundle does. Only the error in a warning names server paths, and hiding the data directory in
// the whole warning cut the front off an entry in a folder named like a relative data directory.
func TestOKFImportWarningsKeepEntryNames(t *testing.T) {
	srv := newRelativeDataDirServer(t, "wiki")
	captureLog(t)

	bundle := okfBundle(t, map[string]string{"wiki/broken.md": "---\ntitle: [unclosed\n---\nbody\n"})
	for via, warnings := range importWarnings(t, srv, bundle) {
		if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "wiki/broken.md: not a valid OKF concept document: ") {
			t.Errorf("%s: want one warning naming wiki/broken.md, got %q", via, warnings)
		}
	}
}

// TestOKFImportSaveFailureWarningHidesDataDir pins that the error in a warning about a document that
// failed to save, which names the path the OS failed on, hides the data directory, while the entry
// name in front of it is left as the bundle has it.
func TestOKFImportSaveFailureWarningHidesDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes do not stop writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to directories whatever their mode")
	}

	for _, variant := range []struct {
		name   string
		newSrv func(*testing.T) *Server
	}{
		{name: "absolute data directory", newSrv: newMCPServer},
		{name: "relative data directory", newSrv: func(t *testing.T) *Server { return newRelativeDataDirServer(t, "wiki") }},
	} {
		t.Run(variant.name, func(t *testing.T) {
			srv := variant.newSrv(t)
			captureLog(t)
			if err := os.Chmod(srv.Storage.ArticleDir, 0555); err != nil {
				t.Fatalf("Chmod failed: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(srv.Storage.ArticleDir, 0755) })

			const entry = "wiki/fresh.md"
			bundle := okfBundle(t, map[string]string{entry: "---\ntitle: Fresh\ntype: Wiki\n---\n# Fresh\n"})
			for via, warnings := range importWarnings(t, srv, bundle) {
				if len(warnings) != 1 {
					t.Errorf("%s: want one warning, got %q", via, warnings)
					continue
				}
				cause, ok := strings.CutPrefix(warnings[0], entry+": save failed: ")
				if !ok {
					t.Errorf("%s: want a save failure naming %s, got %q", via, entry, warnings[0])
					continue
				}
				assertHidesDataDir(t, srv, via, cause, "open articles"+string(filepath.Separator))
			}
		})
	}
}
