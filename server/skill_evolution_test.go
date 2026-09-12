package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedSkill(t *testing.T, s *Storage, title, body string) *Article {
	t.Helper()
	status := "ready"
	art, err := s.SaveArticleWithStatus("", title, body, "d", "", "", "seed", nil, ContentTypeSkill, &status)
	if err != nil {
		t.Fatalf("seeding skill %q failed: %v", title, err)
	}
	return art
}

func TestSkillLockRejectsAgentEditPaths(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Locked Skill", "# locked body")

	locked, err := s.LockSkill(skill.Slug, "human-op")
	if err != nil {
		t.Fatalf("LockSkill failed: %v", err)
	}
	if locked.LockedBy != "human-op" || locked.LockedAt.IsZero() {
		t.Fatalf("lock not recorded: by=%q at=%v", locked.LockedBy, locked.LockedAt)
	}

	// Direct content edit refused, naming locker and timestamp.
	_, err = s.ApplyArticleEdit(skill.Slug, ArticleEdit{Title: skill.Title, Content: "# hijack"})
	var lockedErr *SkillLockedError
	if !errors.As(err, &lockedErr) {
		t.Fatalf("ApplyArticleEdit on locked skill: expected *SkillLockedError, got %v", err)
	}
	if !strings.Contains(lockedErr.Error(), "human-op") {
		t.Errorf("lock error must name the locker, got: %s", lockedErr.Error())
	}
	if !strings.Contains(lockedErr.Error(), locked.LockedAt.UTC().Format("2006")) {
		t.Errorf("lock error must carry the lock timestamp, got: %s", lockedErr.Error())
	}

	// Tag writes and reverts refused too.
	if _, err := s.UpdateArticleTags(skill.Slug, []string{"x"}, 0, ""); !errors.As(err, &lockedErr) {
		t.Errorf("UpdateArticleTags on locked skill: expected *SkillLockedError, got %v", err)
	}
	if _, err := s.RevertArticle(skill.Slug, 1); !errors.As(err, &lockedErr) {
		t.Errorf("RevertArticle on locked skill: expected *SkillLockedError, got %v", err)
	}

	// Unlock restores the edit path.
	unlocked, err := s.UnlockSkill(skill.Slug)
	if err != nil {
		t.Fatalf("UnlockSkill failed: %v", err)
	}
	if unlocked.LockedBy != "" || !unlocked.LockedAt.IsZero() {
		t.Fatalf("unlock did not clear the lock: by=%q at=%v", unlocked.LockedBy, unlocked.LockedAt)
	}
	edited, err := s.ApplyArticleEdit(skill.Slug, ArticleEdit{Title: skill.Title, Content: "# edited", LoadedVersion: unlocked.Version})
	if err != nil {
		t.Fatalf("edit after unlock failed: %v", err)
	}
	if edited.Content != "# edited" {
		t.Errorf("expected edited content, got %q", edited.Content)
	}
}

func TestUnlockedSkillUnaffectedByLockGuard(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Open Skill", "# open body")
	edited, err := s.ApplyArticleEdit(skill.Slug, ArticleEdit{Title: skill.Title, Content: "# v2", LoadedVersion: skill.Version})
	if err != nil {
		t.Fatalf("edit on unlocked skill failed: %v", err)
	}
	if edited.Version != skill.Version+1 {
		t.Errorf("expected version %d, got %d", skill.Version+1, edited.Version)
	}
	if _, err := s.LockSkill("no-such-skill", "op"); err == nil {
		t.Error("LockSkill on missing slug should fail")
	}
	if _, err := s.SaveArticle("", "Plain Wiki", "# w", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seeding wiki failed: %v", err)
	}
	if _, err := s.LockSkill("plain-wiki", "op"); err == nil {
		t.Error("LockSkill on a non-skill should fail")
	}
}

func TestSkillLockFrontMatterRoundTrip(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Roundtrip Skill", "# body")
	locked, err := s.LockSkill(skill.Slug, "roundtrip-op")
	if err != nil {
		t.Fatalf("LockSkill failed: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.ArticleDir, skill.Slug+".md"))
	if err != nil {
		t.Fatalf("read skill file: %v", err)
	}
	if !strings.Contains(string(raw), "locked_by: roundtrip-op") {
		t.Errorf("front matter must carry locked_by, got:\n%s", raw)
	}
	reparsed, err := parseArticleFile(raw, true)
	if err != nil {
		t.Fatalf("reparse failed: %v", err)
	}
	if reparsed.LockedBy != "roundtrip-op" ||
		reparsed.LockedAt.UTC().Format(time.RFC3339) != locked.LockedAt.UTC().Format(time.RFC3339) {
		t.Errorf("lock did not round-trip: by=%q at=%v (want %v)", reparsed.LockedBy, reparsed.LockedAt, locked.LockedAt)
	}
	if reparsed.Type != ContentTypeSkill {
		t.Errorf("reserved type must survive the round trip, got %q", reparsed.Type)
	}
}

func TestSkillCandidatePromoteYieldsNewVersion(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Evolving Skill", "# v1 body")
	if _, err := s.SaveArticle("", "Pattern Note", "# pattern", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seeding pattern note failed: %v", err)
	}
	if _, err := s.LockSkill(skill.Slug, "evo-op"); err != nil {
		t.Fatalf("LockSkill failed: %v", err)
	}
	// Locking is itself a save, so re-read: the candidate links the locked version.
	var err error
	skill, err = s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}

	c, err := s.CreateSkillCandidate(skill.Slug, "# v2 body", "--- a\n+++ b\n@@ -1 +1 @@\n-# v1 body\n+# v2 body\n", []string{"pattern-note"}, "test-agent")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if c.ParentSlug != skill.Slug || c.ParentVersion != skill.Version {
		t.Errorf("candidate parent linkage wrong: %+v (want %s v%d)", c, skill.Slug, skill.Version)
	}
	if c.Status != SkillCandidatePending || c.Proposer != "test-agent" || len(c.PatternSlugs) != 1 {
		t.Errorf("candidate fields wrong: %+v", c)
	}

	// Promotion works even though the skill is locked — it is the sanctioned write path.
	promoted, err := s.PromoteSkillCandidate(c.ID)
	if err != nil {
		t.Fatalf("PromoteSkillCandidate failed: %v", err)
	}
	if promoted.Content != "# v2 body" {
		t.Errorf("promoted content wrong: %q", promoted.Content)
	}
	if promoted.Version != skill.Version+1 {
		t.Errorf("expected new version %d, got %d", skill.Version+1, promoted.Version)
	}
	if !strings.Contains(promoted.EditSummary, c.ID) {
		t.Errorf("skill edit summary must link the candidate, got %q", promoted.EditSummary)
	}
	decided, err := s.GetSkillCandidate(c.ID)
	if err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	}
	if decided.Status != SkillCandidatePromoted || decided.ResultVersion != promoted.Version {
		t.Errorf("candidate not marked promoted with result version: %+v", decided)
	}
	// Double-promote is refused.
	if _, err := s.PromoteSkillCandidate(c.ID); err == nil {
		t.Error("second promote should fail")
	}
}

func TestSkillCandidateRejectLeavesSkillIdentical(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Stable Skill", "# stable body")

	c, err := s.CreateSkillCandidate(skill.Slug, "# rejected body", "", nil, "test-agent")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(s.ArticleDir, skill.Slug+".md"))
	if err != nil {
		t.Fatalf("read skill file: %v", err)
	}
	rejected, err := s.RejectSkillCandidate(c.ID)
	if err != nil {
		t.Fatalf("RejectSkillCandidate failed: %v", err)
	}
	if rejected.Status != SkillCandidateRejected {
		t.Errorf("expected rejected status, got %q", rejected.Status)
	}
	after, err := os.ReadFile(filepath.Join(s.ArticleDir, skill.Slug+".md"))
	if err != nil {
		t.Fatalf("re-read skill file: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("rejected candidate changed the live skill:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	live, err := s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if live.Content != "# stable body" || live.Version != skill.Version {
		t.Errorf("live skill changed: content=%q version=%d", live.Content, live.Version)
	}
}

func TestMcpEditAgentSkillRefusesLockedSkill(t *testing.T) {
	srv := newMCPServer(t)
	status := "ready"
	art, err := srv.Storage.SaveArticleWithStatus("", "MCP Locked Skill", "# body", "", "", "", "seed", nil, ContentTypeSkill, &status)
	if err != nil {
		t.Fatalf("seeding skill failed: %v", err)
	}
	if _, err := srv.Storage.LockSkill(art.Slug, "mcp-op"); err != nil {
		t.Fatalf("LockSkill failed: %v", err)
	}
	resp := toolCall(t, srv, fmt.Sprintf(`{"name":"edit_agent_skill","arguments":{"slug":%q,"content":"# hijack","loaded_version":%d}}`, art.Slug, art.Version))
	if !resp.IsError {
		t.Fatalf("edit_agent_skill on locked skill should be an error, got: %v", resp.Content)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "mcp-op") || !strings.Contains(strings.ToLower(text), "locked") {
		t.Errorf("locked error must name the locker, got: %s", text)
	}
	if !strings.Contains(text, "candidate") {
		t.Errorf("locked error should point at the candidate flow, got: %s", text)
	}
	live, err := srv.Storage.GetArticle(art.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if live.Content != "# body" {
		t.Errorf("locked skill content changed: %q", live.Content)
	}

	// edit_wiki_article against the same locked skill is refused as well.
	resp2 := toolCall(t, srv, fmt.Sprintf(`{"name":"edit_wiki_article","arguments":{"slug":%q,"title":%q,"content":"# hijack2","loaded_version":%d}}`, art.Slug, live.Title, live.Version))
	if !resp2.IsError || !strings.Contains(resp2.Content[0].Text, "mcp-op") {
		t.Errorf("edit_wiki_article on locked skill should refuse naming the locker, got: %+v", resp2)
	}

	// delete is refused as well.
	resp3 := toolCall(t, srv, fmt.Sprintf(`{"name":"delete_wiki_article","arguments":{"slug":%q}}`, art.Slug))
	if !resp3.IsError || !strings.Contains(strings.ToLower(resp3.Content[0].Text), "locked") {
		t.Errorf("delete_wiki_article on locked skill should refuse, got: %+v", resp3)
	}
}

func TestCreateSkillCandidateValidation(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Validated Skill", "# body")
	if _, err := s.CreateSkillCandidate(skill.Slug, "", "", nil, "op"); err == nil {
		t.Error("candidate with neither body nor diff should fail")
	}
	if _, err := s.CreateSkillCandidate(skill.Slug, "# b", "", nil, ""); err == nil {
		t.Error("candidate without proposer should fail")
	}
	if _, err := s.CreateSkillCandidate("missing", "# b", "", nil, "op"); err == nil {
		t.Error("candidate with missing parent should fail")
	}
	if _, err := s.SaveArticle("", "Not A Skill", "# w", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seeding wiki failed: %v", err)
	}
	if _, err := s.CreateSkillCandidate("not-a-skill", "# b", "", nil, "op"); err == nil {
		t.Error("candidate with non-skill parent should fail")
	}
	if _, err := s.GetSkillCandidate("no-such-candidate"); err == nil {
		t.Error("lookup of missing candidate should fail")
	}
	if _, err := s.GetSkillCandidate("../escape"); err == nil {
		t.Error("path-traversal candidate id should fail")
	}
}
