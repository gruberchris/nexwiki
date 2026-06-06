package server

import (
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

	// Channel should be buffered (capacity 100)
	if cap(ch) != 100 {
		t.Errorf("expected channel capacity 100, got %d", cap(ch))
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
