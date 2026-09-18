package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEventBusSubscribeUnsubscribe(t *testing.T) {
	eb := NewEventBus()

	ch := eb.Subscribe()
	if ch == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	// Channel should be buffered (capacity subscriberBufferSize)
	if cap(ch) != subscriberBufferSize {
		t.Errorf("expected channel capacity %d, got %d", subscriberBufferSize, cap(ch))
	}

	// Unsubscribe removes channel and closes it
	eb.Unsubscribe(ch)

	// Channel should be closed after unsubscribe
	select {
	case _, open := <-ch:
		if open {
			t.Error("channel should be closed after Unsubscribe")
		}
	default:
		t.Error("channel should be closed (readable) after Unsubscribe")
	}

	// Double-unsubscribe is safe (no panic)
	eb.Unsubscribe(ch)
}

func TestEventBusPublishActivity(t *testing.T) {
	eb := NewEventBus()
	ch := eb.Subscribe()
	defer eb.Unsubscribe(ch)

	eb.PublishActivity("api", "create", "", "test-slug", "Test Article", "User")

	select {
	case msg := <-ch:
		if !strings.Contains(msg, "event: activity") {
			t.Errorf("expected 'event: activity' in message, got: %s", msg)
		}
		if !strings.Contains(msg, "test-slug") {
			t.Errorf("expected slug in message, got: %s", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for published activity event")
	}
}

func TestEventBusPublishActivityDeduplication(t *testing.T) {
	eb := NewEventBus()
	ch := eb.Subscribe()
	defer eb.Unsubscribe(ch)

	// Publish identical event twice within 2-second window
	eb.PublishActivity("api", "create", "", "slug", "Title", "User")
	eb.PublishActivity("api", "create", "", "slug", "Title", "User")

	// Drain the channel
	count := 0
	timeout := time.After(50 * time.Millisecond)
	for {
		select {
		case <-ch:
			count++
		case <-timeout:
			goto done
		}
	}
done:
	if count != 1 {
		t.Errorf("expected 1 event after deduplication, got %d", count)
	}
}

func TestEventBusPublishWikiUpdate(t *testing.T) {
	eb := NewEventBus()
	ch := eb.Subscribe()
	defer eb.Unsubscribe(ch)

	eb.PublishWikiUpdate(WikiUpdate{
		Type:  "article-added",
		Slug:  "new-article",
		Title: "New Article",
	})

	select {
	case msg := <-ch:
		if !strings.Contains(msg, "event: wiki-update") {
			t.Errorf("expected 'event: wiki-update' in message, got: %s", msg)
		}
		if !strings.Contains(msg, "article-added") {
			t.Errorf("expected update type in message, got: %s", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for wiki-update event")
	}
}

func TestEventBusGetHistory(t *testing.T) {
	eb := NewEventBus()

	// Empty bus
	history := eb.GetHistory()
	if len(history) != 0 {
		t.Errorf("expected empty history, got %d items", len(history))
	}

	// After publishes, history accumulates
	eb.PublishActivity("api", "create", "", "slug1", "Article 1", "User")
	time.Sleep(5 * time.Millisecond) // ensure dedup window differs
	eb.PublishActivity("api", "edit", "", "slug2", "Article 2", "User")
	time.Sleep(5 * time.Millisecond)
	eb.PublishActivity("api", "delete", "", "slug3", "Article 3", "User")

	history2 := eb.GetHistory()
	if len(history2) != 3 {
		t.Errorf("expected 3 history events, got %d", len(history2))
	}

	// Verify order (oldest first)
	if history2[0].Action != "create" {
		t.Errorf("expected first event action 'create', got '%s'", history2[0].Action)
	}
	if history2[2].Action != "delete" {
		t.Errorf("expected last event action 'delete', got '%s'", history2[2].Action)
	}

	// Result is a copy: mutations don't affect the bus buffer
	history2[0].Action = "mutated"
	history3 := eb.GetHistory()
	if history3[0].Action == "mutated" {
		t.Error("GetHistory should return a copy, not a reference to the internal buffer")
	}
}

// drainSSEFrames empties a browser subscriber channel, returning the frames it held and whether
// any of them was the missed-events marker.
func drainSSEFrames(ch chan string) (frames []string, markers int) {
	for {
		select {
		case frame := <-ch:
			frames = append(frames, frame)
			if strings.HasPrefix(frame, "event: missed-events\n") {
				markers++
			}
		default:
			return frames, markers
		}
	}
}

// TestSubscriberBufferAbsorvesBulkBatch pins the #172 scenario: a bulk operation announces one
// activity event and one live update per changed document, interleaved on a browser tab's one
// channel. The 300-document tag deletion from the issue is 600 frames, and all of them must
// arrive — no marker, no drop.
func TestSubscriberBufferAbsorvesBulkBatch(t *testing.T) {
	eb := NewEventBus()
	ch := eb.Subscribe()
	defer eb.Unsubscribe(ch)

	const docs = 300
	for i := 0; i < docs; i++ {
		slug := fmt.Sprintf("doc-%d", i)
		eb.PublishActivity("api", "edit", "delete_tag", slug, "Title", "User")
		eb.PublishWikiUpdate(WikiUpdate{Type: "article-edited", Slug: slug, Title: "Title"})
	}

	frames, markers := drainSSEFrames(ch)
	if markers != 0 {
		t.Errorf("a batch the buffer absorbs still produced %d missed-events markers", markers)
	}
	if len(frames) != 2*docs {
		t.Errorf("expected all %d frames of the batch, got %d", 2*docs, len(frames))
	}
}

// TestOverflowDeliversMissedEventsMarker pins the delivery guarantee the marker exists for: a
// subscriber that falls behind a batch larger than its buffer must reliably learn it missed
// events, even though the buffer was full when the overflow happened.
func TestOverflowDeliversMissedEventsMarker(t *testing.T) {
	eb := NewEventBus()
	ch := eb.Subscribe()
	defer eb.Unsubscribe(ch)

	// More events than the buffer holds, published while the subscriber never reads.
	const batch = subscriberBufferSize + 50
	for i := 0; i < batch; i++ {
		eb.PublishActivity("api", "edit", "", fmt.Sprintf("doc-%d", i), "Title", "User")
	}

	frames, markers := drainSSEFrames(ch)
	if markers == 0 {
		t.Fatal("the subscriber overflowed but was never told it missed events")
	}
	if len(frames) >= batch {
		t.Errorf("the collapsed backlog still delivered all %d events; nothing was dropped, so the marker is untestable", batch)
	}
	// The marker replaced the backlog, so the subscriber heard about the very events it lost.
	for _, frame := range frames {
		if frame != missedEventsFrame && !strings.HasPrefix(frame, "event: activity\n") {
			t.Errorf("unexpected frame on the activity channel: %q", frame)
		}
	}
}

// TestOverflowMarksWikiUpdateSubscribers is the same guarantee for the structured channel MCP
// subscriptions listen on: the marker is a WikiUpdate the subscription stream can map onto
// notifications, not a dropped update.
func TestOverflowMarksWikiUpdateSubscribers(t *testing.T) {
	eb := NewEventBus()
	ch := eb.SubscribeWikiUpdates()
	defer eb.UnsubscribeWikiUpdates(ch)

	const batch = subscriberBufferSize + 50
	for i := 0; i < batch; i++ {
		eb.PublishWikiUpdate(WikiUpdate{Type: "article-edited", Slug: fmt.Sprintf("doc-%d", i), Title: "Title"})
	}

	markers := 0
	frames := 0
	for {
		select {
		case u := <-ch:
			frames++
			if u.Type != UpdateTypeMissed && u.Type != "article-edited" {
				t.Errorf("unexpected update on the wiki channel: %+v", u)
			}
			if u.Type == UpdateTypeMissed {
				markers++
			}
		default:
			if markers == 0 {
				t.Fatal("the wiki subscriber overflowed but was never marked")
			}
			if frames >= batch {
				t.Errorf("the collapsed backlog still delivered all %d updates", batch)
			}
			return
		}
	}
}

// TestPublishActivityVersionDeduplication pins the version-qualified key (#173): two legitimate
// changes to one document inside the 2-second window differ in version and both survive, while
// one save announced twice — same version — still collapses.
func TestPublishActivityVersionDeduplication(t *testing.T) {
	eb := NewEventBus()

	// Two quick edits of the same document by the same agent: both revisions get their event.
	eb.PublishActivityVersion("mcp", "edit", "edit_wiki_article", "doc", "Doc", "Agent", 2)
	eb.PublishActivityVersion("mcp", "edit", "edit_wiki_article", "doc", "Doc", "Agent", 3)
	if h := eb.GetHistory(); len(h) != 2 {
		t.Fatalf("two revisions inside the window produced %d events, want 2", len(h))
	}

	// The same save announced twice: one event.
	eb.PublishActivityVersion("mcp", "edit", "edit_wiki_article", "doc", "Doc", "Agent", 5)
	eb.PublishActivityVersion("mcp", "edit", "edit_wiki_article", "doc", "Doc", "Agent", 5)
	if h := eb.GetHistory(); len(h) != 3 {
		t.Errorf("a duplicate of one save produced %d events total, want 3", len(h))
	}

	// A versioned event and an unversioned one are never the same save, so both are kept.
	eb.PublishActivityVersion("api", "edit", "", "doc", "Doc", "User", 7)
	eb.PublishActivity("lifecycle", "edit", "plan_lifecycle", "doc", "Doc", "NexWiki")
	if h := eb.GetHistory(); len(h) != 5 {
		t.Errorf("versioned/unversioned pair produced %d events total, want 5", len(h))
	}

	// The version rides on the event, so the durable log and the wire carry it too.
	if v := eb.GetHistory()[3].Version; v != 7 {
		t.Errorf("event version %d, want 7", v)
	}
	if v := eb.GetHistory()[4].Version; v != 0 {
		t.Errorf("unversioned event carried version %d, want 0", v)
	}
}
