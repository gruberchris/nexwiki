package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The sweep behind a global tag deletion takes writeMu per document rather than across the whole
// corpus (see DeleteTagGlobally), so these tests choreograph the interleavings that change:
// an unrelated write landing mid-sweep, a concurrent edit to a document the sweep still has
// queued, a Close that begins mid-sweep, and the announcement a handler makes of what the sweep
// actually rewrote. The choreography is the write lock handed back and forth under the tests'
// control, which the per-document holds make possible — under the old whole-sweep hold there is
// no "between documents" for a test to stand in.

type sweepResult struct {
	report *TagDeletionReport
	err    error
}

// startTagSweep starts DeleteTagGlobally(tag) on s in a goroutine while the test already holds
// writeMu, and returns the channel its report and error arrive on. The sweep parks on its first
// document's lock; pumpTagSweep advances it from there.
func startTagSweep(t *testing.T, s *Storage, tag string) <-chan sweepResult {
	t.Helper()
	out := make(chan sweepResult, 1)
	go func() {
		report, err := s.DeleteTagGlobally(tag)
		out <- sweepResult{report, err}
	}()
	return out
}

// countClean reports how many of slugs currently carry no case-insensitive variant of tag.
func countClean(t *testing.T, s *Storage, slugs []string, tag string) int {
	t.Helper()
	n := 0
	for _, slug := range slugs {
		art, err := s.GetArticle(slug)
		if err != nil {
			t.Fatalf("GetArticle(%s): %v", slug, err)
		}
		if !slices.ContainsFunc(art.Tags, func(x string) bool { return strings.EqualFold(x, tag) }) {
			n++
		}
	}
	return n
}

// pumpTagSweep hands the write lock to a running global tag deletion sweep until at least want
// of slugs have lost every variant of tag, then returns the clean count and leaves the test
// holding the lock with the sweep parked between documents.
//
// The caller must hold writeMu on entry and the sweep must be running (parked on the lock). Each
// loop round releases the lock — handing it to the sweep, which rewrites one document and
// releases it in turn — and takes it back before counting, so a count never races a rewrite and
// the sweep is parked again on return. A round can let the sweep slip through more than one
// document before the test's lock wins the handoff back, so the returned count may exceed want;
// the counts it returns are still exact, measured under the lock the sweep cannot hold.
func pumpTagSweep(t *testing.T, s *Storage, slugs []string, tag string, want int) int {
	t.Helper()
	if want < 0 || want > len(slugs) {
		t.Fatalf("cannot pump to %d clean of %d documents", want, len(slugs))
	}
	deadline := time.Now().Add(testWaitLimit)
	for {
		if n := countClean(t, s, slugs, tag); n >= want {
			return n
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the sweep did not reach %d clean documents in time", want)
		}
		s.writeMu.Unlock()
		s.writeMu.Lock()
	}
}

// receiveSweep blocks for a sweep's result, failing the test rather than hanging if none comes.
func receiveSweep(t *testing.T, what string, ch <-chan sweepResult) sweepResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(testWaitLimit):
		t.Fatalf("%s did not finish in time", what)
		return sweepResult{}
	}
}

// TestDeleteTagGloballyYieldsToConcurrentWrites pins the reason the lock is chunked: while a
// sweep of many documents is running, an unrelated save waits out a document or two, not the
// whole sweep. The bound is measured against a single-document save of the same document on the
// same storage, so the test says "a few times one save", not some absolute duration. A
// concurrent edit to a document still queued by the sweep is also exercised: whichever of the
// two writes it last, the document ends up without the tag and with the edit's content, whole.
func TestDeleteTagGloballyYieldsToConcurrentWrites(t *testing.T) {
	const carriers = 24
	s := newLifecycleStorage(t)

	slugs := make([]string, 0, carriers)
	for i := 0; i < carriers; i++ {
		art, err := s.SaveArticle("", "Swept "+strings.Repeat("a", i), "# body", "", "", "", "", []string{"swept"}, ContentTypeWiki)
		if err != nil {
			t.Fatalf("seed carrier %d: %v", i, err)
		}
		slugs = append(slugs, art.Slug)
	}
	edited := slugs[12]
	bystander, err := s.SaveArticle("", "Bystander", "# v1", "", "", "", "", nil, ContentTypeWiki)
	if err != nil {
		t.Fatalf("seed bystander: %v", err)
	}

	// The single-document baseline: one full save, history snapshot and index update, with the
	// storage otherwise idle.
	baseStart := time.Now()
	if _, err = s.SaveArticle(bystander.Slug, bystander.Title, "# v2", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("baseline save: %v", err)
	}
	baseline := time.Since(baseStart)

	s.writeMu.Lock()
	done := startTagSweep(t, s, "swept")
	pumped := pumpTagSweep(t, s, slugs, "swept", 1)
	if pumped >= carriers {
		t.Fatalf("the sweep finished inside the pump window; the test needs documents still queued")
	}

	// The sweep is at least one document in and parked on the next of 24. An ordinary save now —
	// the thing that used to wait behind all 24 — must cost about what one save costs.
	s.writeMu.Unlock()
	start := time.Now()
	if _, err = s.SaveArticle(bystander.Slug, bystander.Title, "# v3", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("concurrent save: %v", err)
	}
	waited := time.Since(start)

	s.writeMu.Lock()
	pumpTagSweep(t, s, slugs, "swept", 20)

	// A concurrent edit to a document the sweep still has queued: its tags replace the tag the
	// sweep would remove and its content is its own. Whichever of the two writes the document
	// last, it ends without the tag and with the edit's content.
	concurrentEdit := make(chan error, 1)
	go func() {
		_, err := s.SaveArticle(edited, "Swept "+strings.Repeat("a", 12), "# concurrent edit", "", "", "", "", []string{"kept"}, ContentTypeWiki)
		concurrentEdit <- err
	}()

	pumpTagSweep(t, s, slugs, "swept", carriers)
	// The pump returns holding the lock; a concurrent edit still parked on it needs it released
	// before anything can wait for the edit to have finished.
	s.writeMu.Unlock()

	res := receiveSweep(t, "the sweep", done)
	if res.err != nil {
		t.Fatalf("DeleteTagGlobally: %v", res.err)
	}
	select {
	case err := <-concurrentEdit:
		if err != nil {
			t.Fatalf("concurrent edit: %v", err)
		}
	case <-time.After(testWaitLimit):
		t.Fatalf("the concurrent edit did not finish with the sweep")
	}

	// The concurrent save paid for at most a document or two of the sweep plus its own write.
	// The old whole-sweep hold would have parked it behind more than twenty of them — well past
	// eight single-document saves.
	if waited > 8*baseline {
		t.Errorf("a concurrent save waited %v behind the sweep, over eight single-save times (%v)", waited, baseline)
	}

	// Every carrier lost the tag; the edited one carries the edit, not the sweep's version of it.
	if n := countClean(t, s, slugs, "swept"); n != carriers {
		t.Errorf("%d of %d carriers lost the tag", n, carriers)
	}
	after, err := s.GetArticle(edited)
	if err != nil || after.Content != "# concurrent edit" || !slices.Equal(after.Tags, []string{"kept"}) {
		t.Errorf("the concurrently edited document is not the edit's: %+v (%v)", after, err)
	}
	// The bystander is the concurrent save's version, and the sweep never touched it.
	if art, err := s.GetArticle(bystander.Slug); err != nil || art.Version != 3 || art.Content != "# v3" {
		t.Errorf("the bystander is not the concurrent save's version 3: %+v (%v)", art, err)
	}
	// The sweep rewrote every carrier except possibly the one the concurrent edit wrote first —
	// a skip is the correct outcome there, not a lost document.
	if len(res.report.Rewritten) != carriers && len(res.report.Rewritten) != carriers-1 {
		t.Errorf("the sweep reports %d rewritten, want %d (or %d if the concurrent edit won the race for one): %+v",
			len(res.report.Rewritten), carriers, carriers-1, res.report.Rewritten)
	}
	if len(res.report.Skipped) != 0 || len(res.report.Failed) != 0 {
		t.Errorf("nothing was skipped or failed: skipped %v, failed %+v", res.report.Skipped, res.report.Failed)
	}
}

// TestDeleteTagGloballyStoppedByCloseMidSweepAndRerunFinishes pins the partial state a shutdown
// leaves: exactly the documents rewritten before Close began are clean, the sweep returns
// ErrStorageClosed with a report saying so (and the log says what completed), and rerunning the
// deletion — here on a fresh Storage over the same data directory, the shape an interrupted
// delete actually resumes in — finishes the remainder without rewriting anything it had already
// done.
func TestDeleteTagGloballyStoppedByCloseMidSweepAndRerunFinishes(t *testing.T) {
	const total, minDone = 30, 3
	s := newLifecycleStorage(t)
	dataDir := s.DataDir

	slugs := make([]string, 0, total)
	for i := 0; i < total; i++ {
		art, err := s.SaveArticle("", "Doomed "+strings.Repeat("a", i), "# body", "", "", "", "", []string{"doomed"}, ContentTypeWiki)
		if err != nil {
			t.Fatalf("seed carrier %d: %v", i, err)
		}
		slugs = append(slugs, art.Slug)
	}

	buf := captureLog(t)
	s.writeMu.Lock()
	done := startTagSweep(t, s, "doomed")
	// The count this returns is exact — measured while the test holds the lock and the sweep is
	// parked — and cannot move again until the lock is released below, after closed is set.
	doneBeforeClose := pumpTagSweep(t, s, slugs, "doomed", minDone)
	if doneBeforeClose >= total {
		t.Fatalf("the sweep finished inside the pump window; the test needs a partial state to interrupt")
	}

	// Close begins while the sweep is parked between documents (the test holds the lock, so the
	// count cannot move). closed is set before Close waits for the lock, so the sweep's next
	// hold — the moment the test releases — sees it and stops.
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close() }()
	for !s.closed.Load() {
		time.Sleep(time.Millisecond)
	}
	s.writeMu.Unlock()

	res := receiveSweep(t, "the sweep stopped by Close", done)
	if !errors.Is(res.err, ErrStorageClosed) {
		t.Fatalf("a sweep stopped by Close returned %v, want ErrStorageClosed", res.err)
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	case <-time.After(testWaitLimit):
		t.Fatalf("Close did not finish once the sweep stopped")
	}

	if n := countClean(t, s, slugs, "doomed"); n != doneBeforeClose {
		t.Errorf("%d documents are clean after the interrupted sweep, want exactly %d", n, doneBeforeClose)
	}
	if len(res.report.Rewritten) != doneBeforeClose {
		t.Errorf("the report lists %d rewritten, want the %d done before Close: %+v",
			len(res.report.Rewritten), doneBeforeClose, res.report.Rewritten)
	}
	if len(res.report.Skipped) != 0 || len(res.report.Failed) != 0 {
		t.Errorf("nothing was skipped or failed: skipped %v, failed %+v", res.report.Skipped, res.report.Failed)
	}
	if line := buf.String(); !strings.Contains(line, "stopped early") || !strings.Contains(line, "doomed") ||
		!strings.Contains(line, fmt.Sprintf("%d documents rewritten", doneBeforeClose)) {
		t.Errorf("the interrupted sweep does not log what completed: %q", line)
	}

	// The rerun, on a storage opened over the same data directory: the original storage is
	// closed and cannot be reopened in place, and that is the shape an interrupted delete
	// actually resumes in.
	s2, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("reopen storage: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	report, err := s2.DeleteTagGlobally("doomed")
	if err != nil {
		t.Fatalf("the rerun must finish the remainder: %v", err)
	}
	if len(report.Rewritten) != total-doneBeforeClose {
		t.Errorf("the rerun rewrote %d, want the remaining %d: %+v", len(report.Rewritten), total-doneBeforeClose, report.Rewritten)
	}
	if n := countClean(t, s2, slugs, "doomed"); n != total {
		t.Errorf("%d of %d documents lost the tag after the rerun", n, total)
	}
	// Every document ends at version 2 — one rewrite each — and the rerun rewrote exactly the
	// documents the interrupted sweep did not: a document already rewritten carries no variant
	// of the tag, so the rerun skips it rather than rewriting it again.
	rewrittenFirst := make(map[string]bool, len(res.report.Rewritten))
	for _, d := range res.report.Rewritten {
		rewrittenFirst[d.Slug] = true
	}
	for _, d := range report.Rewritten {
		if rewrittenFirst[d.Slug] {
			t.Errorf("the rerun rewrote %s again, which the interrupted sweep had already cleaned", d.Slug)
		}
	}
	for _, slug := range slugs {
		art, err := s2.GetArticle(slug)
		if err != nil || art.Version != 2 {
			t.Errorf("%s is at version %d, want exactly one rewrite (version 2): %+v (%v)", slug, art.Version, art, err)
		}
	}

	// And a rerun with nothing left to do is a clean no-op.
	report, err = s2.DeleteTagGlobally("doomed")
	if err != nil {
		t.Fatalf("a no-op rerun failed: %v", err)
	}
	if len(report.Rewritten) != 0 || len(report.Skipped) != 0 || len(report.Failed) != 0 {
		t.Errorf("a no-op rerun did something: %+v", report)
	}
}

// TestDeleteTagGloballyIsANoopForAnAbsentTag pins the cleanest rerun: a tag nothing carries
// rewrites nothing, skips nothing, and fails nothing, with no error to suggest otherwise.
func TestDeleteTagGloballyIsANoopForAnAbsentTag(t *testing.T) {
	s := newLifecycleStorage(t)
	if _, err := s.SaveArticle("", "Untagged", "# body", "", "", "", "", []string{"keep"}, ContentTypeWiki); err != nil {
		t.Fatalf("seed: %v", err)
	}
	report, err := s.DeleteTagGlobally("never-existed")
	if err != nil {
		t.Fatalf("deleting an absent tag: %v", err)
	}
	if report == nil {
		t.Fatalf("deleting an absent tag returned no report")
	}
	if report.Tag != "never-existed" || len(report.Rewritten) != 0 || len(report.Skipped) != 0 || len(report.Failed) != 0 {
		t.Errorf("a no-op deletion did something: %+v", report)
	}
}

// TestDeleteTagGloballyReportsTheFailedDocument pins the other early stop: the first document
// whose save fails ends the sweep, is named in the error, and is reported separately from the
// documents already rewritten. The broken fixture is a plan whose status no longer validates —
// the read succeeds, so the sweep reaches the save, which refuses it.
func TestDeleteTagGloballyReportsTheFailedDocument(t *testing.T) {
	s := newLifecycleStorage(t)
	// Written directly so the timestamps fix the sweep order (newest first) and the plan can
	// carry a status that no save accepts.
	docs := map[string]string{
		"good-doc":  "---\ntype: Wiki\ntitle: Good Doc\nslug: good-doc\ntags:\n  - removable\ntimestamp: \"2022-01-01T00:00:00Z\"\nversion: 1\n---\n# g\n",
		"bad-plan":  "---\ntype: AI-Agent-Plan\ntitle: Bad Plan\nslug: bad-plan\nstatus: bogus\ntags:\n  - removable\ntimestamp: \"2021-01-01T00:00:00Z\"\nversion: 1\n---\n# b\n",
		"later-doc": "---\ntype: Wiki\ntitle: Later Doc\nslug: later-doc\ntags:\n  - removable\ntimestamp: \"2020-01-01T00:00:00Z\"\nversion: 1\n---\n# l\n",
	}
	for slug, doc := range docs {
		if err := os.WriteFile(filepath.Join(s.ArticleDir, slug+".md"), []byte(doc), 0644); err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
	}

	report, err := s.DeleteTagGlobally("removable")
	if err == nil || !strings.Contains(err.Error(), "bad-plan") {
		t.Fatalf("the failed save must stop the sweep and name the document, got %v", err)
	}
	if len(report.Rewritten) != 1 || report.Rewritten[0].Slug != "good-doc" {
		t.Errorf("the documents rewritten before the failure are not reported: %+v", report.Rewritten)
	}
	if len(report.Failed) != 1 || report.Failed[0].Slug != "bad-plan" || !strings.Contains(report.Failed[0].Err, "bogus") {
		t.Errorf("the failed document is not reported: %+v", report.Failed)
	}
	if len(report.Skipped) != 0 {
		t.Errorf("nothing was skipped: %v", report.Skipped)
	}
	// The sweep stopped: the document after the failure still carries the tag.
	if art, err := s.GetArticle("later-doc"); err != nil || !slices.Contains(art.Tags, "removable") {
		t.Errorf("the sweep was expected to stop before 'later-doc': %+v (%v)", art, err)
	}
}

// TestHandleDeleteTagGloballyAnnouncesWhatTheSweepRewrote pins the announcement race the
// version-comparing pre-scan had: a carrier whose version moves without the sweep rewriting it
// must not be announced. The handler must announce what the sweep's report says it rewrote —
// nothing else — and this test is what breaks if a pre-scan comes back.
//
// The moved version is produced without a goroutine: "cold" is listed from the article cache
// (its metadata was parsed before an edit landed), while the sweep re-reads the file itself.
// To a pre-scan comparing listed versions, cold's jump from 1 to 2 is indistinguishable from a
// sweep rewrite — exactly the misread an interleaved edit caused — but the sweep re-reads cold
// under the lock, finds no variant of the tag, and skips it. The bytes are swapped behind the
// cache the way corruptBehindCache swaps them: same size, same pinned mtime, so the listing goes
// on serving the old metadata while the file is the edit's.
func TestHandleDeleteTagGloballyAnnouncesWhatTheSweepRewrote(t *testing.T) {
	srv := newTestServer(t)
	coldTime := time.Now().Add(-2 * time.Hour)
	// The sweep visits newest first, so "hot" goes first and "cold" is still queued behind it.
	writeArticleFile(t, srv.Storage, "hot.md", "type: Wiki\ntitle: Hot\nslug: hot\ntags:\n  - removable\ntimestamp: \"2022-01-01T00:00:00Z\"\nversion: 1\n", "# hot\n", time.Now().Add(-time.Hour))
	writeArticleFile(t, srv.Storage, "cold.md", "type: Wiki\ntitle: Cold\nslug: cold\ntags:\n  - removable\ntimestamp: \"2021-01-01T00:00:00Z\"\nversion: 1\n", "# cold (v1)\n", coldTime)

	// Warm the cache with cold as a carrier at version 1, then let the interleaved writer land:
	// same byte length (removable → keepalive, 1 → 2, v1 → v2), same pinned mtime.
	if _, err := srv.Storage.ListArticles(); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}
	writeArticleFile(t, srv.Storage, "cold.md", "type: Wiki\ntitle: Cold\nslug: cold\ntags:\n  - keepalive\ntimestamp: \"2021-01-01T00:00:00Z\"\nversion: 2\n", "# cold (v2)\n", coldTime)

	obs := observeBus(t, srv)
	req := httptest.NewRequest("DELETE", "/api/tags/removable", nil)
	req.SetPathValue("tag", "removable")
	w := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Message   string `json:"message"`
		Rewritten int    `json:"rewritten"`
		Skipped   int    `json:"skipped"`
		Failed    int    `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response: %s", w.Body.String())
	}
	if body.Rewritten != 1 || body.Skipped != 0 || body.Failed != 0 {
		t.Errorf("the sweep rewrote only 'hot', but the response says otherwise: %+v", body)
	}

	// "hot" was rewritten by the sweep and is announced; "cold" was rewritten by the interleaved
	// writer and is not — announcing it would report a rewrite the sweep never made.
	events := obs.activity(t)
	if got := sortedKeys(events); !slices.Equal(got, []string{"hot"}) {
		t.Errorf("activity events for %v, want only the document the sweep rewrote", got)
	}
	if u := obs.wikiUpdates(t, 1)["hot"]; u.Type != "article-edited" {
		t.Errorf("unexpected wiki update for 'hot': %+v", u)
	}
	// And "cold" is exactly the interleaved writer's document: the sweep skipped it rather than
	// overwriting the edit, and it kept the tag the writer left.
	cold, err := srv.Storage.GetArticle("cold")
	if err != nil || cold.Version != 2 || cold.Content != "# cold (v2)" || !slices.Equal(cold.Tags, []string{"keepalive"}) {
		t.Errorf("'cold' is not the interleaved writer's document: %+v (%v)", cold, err)
	}
}
