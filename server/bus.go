package server

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// subscriberBufferSize is the buffer each live subscriber gets, in messages.
//
// Bulk operations (an OKF import, a global tag deletion) publish one activity event plus one live
// wiki update per changed document, and a browser tab receives both on one channel — the
// 300-document tag deletion that motivated this was a 600-message burst against a 100-slot
// buffer, and the tail of it was silently dropped (#172). 1024 slots absorbs a ~500-document
// batch of both message kinds, an order of magnitude above the reported case, while staying
// modest per subscriber: a buffered channel costs its slots' worth of memory (16 KiB of channel
// headers plus the burst's payload bytes, held only while the subscriber is behind), and a wiki
// has a handful of open tabs and MCP subscriptions, not thousands.
const subscriberBufferSize = 1024

// missedEventsFrame is the SSE frame that replaces a browser subscriber's collapsed backlog
// after an overflow. It is its own event name, not an "activity" or "wiki-update" frame, so the
// frontend handles it as what it is — a "you fell behind, reload" signal — rather than as an
// event to render.
const missedEventsFrame = "event: missed-events\ndata: {\"type\":\"missed-events\"}\n\n"

// EventBus implements a thread-safe circular event buffer and the "pub-sub" model for real-time SSE broadcasts.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan string]bool
	buffer      []LogEvent
	bufferLimit int
	eventCount  int
	persist     func(LogEvent)

	// wikiSubscribers receive WikiUpdate values rather than pre-formatted SSE frames. The browser
	// wants a ready-to-write SSE string; MCP subscribers need the structured event so they can map
	// a slug onto a resource URI and decide which notification type it warrants. Serving both from
	// the same pre-formatted string would mean re-parsing our own output.
	wikiSubscribers map[chan WikiUpdate]bool
}

// SetPersist registers a callback invoked once for every published (non-deduplicated)
// activity event, used to durably persist events outside the in-memory ring buffer.
func (eb *EventBus) SetPersist(fn func(LogEvent)) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.persist = fn
}

// NewEventBus builds a thread-safe pub-sub manager.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers:     make(map[chan string]bool),
		wikiSubscribers: make(map[chan WikiUpdate]bool),
		buffer:          make([]LogEvent, 0, 200),
		bufferLimit:     200,
	}
}

// Subscribe creates a channel registered to receive direct string messages.
func (eb *EventBus) Subscribe() chan string {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan string, subscriberBufferSize)
	eb.subscribers[ch] = true
	return ch
}

// Unsubscribe removes a channel from the active broadcast collection.
func (eb *EventBus) Unsubscribe(ch chan string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if _, exists := eb.subscribers[ch]; exists {
		delete(eb.subscribers, ch)
		close(ch)
	}
}

// SubscribeWikiUpdates registers a channel receiving structured article change events.
// Used by MCP subscriptions/listen streams; the browser SSE path uses Subscribe instead.
func (eb *EventBus) SubscribeWikiUpdates() chan WikiUpdate {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan WikiUpdate, subscriberBufferSize)
	eb.wikiSubscribers[ch] = true
	return ch
}

// UnsubscribeWikiUpdates removes a structured subscriber and closes its channel.
func (eb *EventBus) UnsubscribeWikiUpdates(ch chan WikiUpdate) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if _, exists := eb.wikiSubscribers[ch]; exists {
		delete(eb.wikiSubscribers, ch)
		close(ch)
	}
}

// PublishActivity commits a new LogEvent, appends it to the circular queue, and broadcasts it to
// all listeners. Use for events that are not tied to a document revision: reads, lifecycle
// worker actions, deletes that carry no surviving version.
func (eb *EventBus) PublishActivity(source, action, tool, slug, title, agent string) {
	eb.publishActivity(source, action, tool, slug, title, agent, 0)
}

// PublishActivityVersion commits a new LogEvent for a write that knows the document revision it
// acted on, and qualifies the dedup key with that revision (#173).
//
// Every save produces a new revision, so two legitimate changes to the same document inside the
// 2-second window differ in version and both are kept — previously the second lost its event,
// and with it the attribution get_article_history joins a revision to. A double-emitted event for
// one save carries the same version and still collapses. Pass the version the storage call
// returned; for a delete, the version the document had — the point is only that two events for
// the same slug describe the same change exactly when the version matches. Version 0 means "not
// tied to a revision" and keeps PublishActivity's behavior.
func (eb *EventBus) PublishActivityVersion(source, action, tool, slug, title, agent string, version int) {
	eb.publishActivity(source, action, tool, slug, title, agent, version)
}

func (eb *EventBus) publishActivity(source, action, tool, slug, title, agent string, version int) {
	eb.mu.Lock()

	// Prevent duplicate events within a 2-second window. The version is part of the identity:
	// every field matching plus a matching version is one save announced twice, while a different
	// version is a second, legitimate change to the same document.
	now := time.Now()
	for i := len(eb.buffer) - 1; i >= 0; i-- {
		prev := eb.buffer[i]
		if now.Sub(prev.Timestamp) > 2*time.Second {
			break
		}
		if prev.Source == source &&
			prev.Action == action &&
			prev.Tool == tool &&
			prev.Slug == slug &&
			prev.Agent == agent &&
			prev.Version == version {
			eb.mu.Unlock()
			return
		}
	}

	eb.eventCount++
	event := LogEvent{
		ID:        fmt.Sprintf("evt_%d_%d", now.UnixNano(), eb.eventCount),
		Timestamp: now,
		Source:    source,
		Action:    action,
		Tool:      tool,
		Slug:      slug,
		Title:     title,
		Agent:     agent,
		Version:   version,
	}

	// Add to circular buffer
	if len(eb.buffer) >= eb.bufferLimit {
		eb.buffer = eb.buffer[1:]
	}
	eb.buffer = append(eb.buffer, event)

	data, err := json.Marshal(event)
	persist := eb.persist
	eb.mu.Unlock()

	if persist != nil {
		persist(event)
	}
	if err == nil {
		eb.broadcast("activity", string(data))
	}
}

// PublishWikiUpdate sends a count-synchronization payload to all active clients.
func (eb *EventBus) PublishWikiUpdate(update WikiUpdate) {
	data, err := json.Marshal(update)
	if err == nil {
		eb.broadcast("wiki-update", string(data))
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for ch := range eb.wikiSubscribers {
		deliverOrMark(ch, update, WikiUpdate{Type: UpdateTypeMissed})
	}
}

// GetHistory returns a thread-safe copy of the circular queue buffer.
func (eb *EventBus) GetHistory() []LogEvent {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	res := make([]LogEvent, len(eb.buffer))
	copy(res, eb.buffer)
	return res
}

func (eb *EventBus) broadcast(eventType, data string) {
	ssePayload := fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data)
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for ch := range eb.subscribers {
		deliverOrMark(ch, ssePayload, missedEventsFrame)
	}
}

// deliverOrMark hands msg to a buffered subscriber channel, never blocking and never leaving the
// subscriber ignorant that something was dropped (#172).
//
// While the channel has room, delivery is a plain send. When it is full the subscriber is
// already behind and more messages are arriving, so the buffered backlog — every item of which
// durable state supersedes — is collapsed into a single "missed events" marker, which the
// subscriber acts on by reloading from that durable state. The marker itself is undroppable:
// draining the backlog makes its slot, and if a concurrent publisher wins the freed space
// between the drain and the send, the loop drains again — each iteration discards one buffered
// item, so the send succeeds after finitely many tries.
//
// Callers hold at least eb.mu.RLock, and a channel is only closed under the full lock in
// Unsubscribe, so the channel cannot be closed mid-delivery.
func deliverOrMark[T any](ch chan T, msg, missed T) {
	select {
	case ch <- msg:
		return
	default:
	}
	for {
		drained := false
		for !drained {
			select {
			case <-ch:
			default:
				drained = true
			}
		}
		select {
		case ch <- missed:
			return
		default:
			// A concurrent publisher refilled the freed space first; collapse again.
		}
	}
}
