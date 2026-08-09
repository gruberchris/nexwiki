package server

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

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

	ch := make(chan string, 100)
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

	ch := make(chan WikiUpdate, 100)
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

// PublishActivity commits a new LogEvent, appends it to the circular queue, and broadcasts it to all listeners.
func (eb *EventBus) PublishActivity(source, action, tool, slug, title, agent string) {
	eb.mu.Lock()

	// Prevent duplicate events within a 2-second window
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
			prev.Agent == agent {
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
		select {
		case ch <- update:
		default:
			// Drop rather than block: one stalled MCP subscriber must not wedge a wiki write.
		}
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
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	ssePayload := fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data)
	for ch := range eb.subscribers {
		select {
		case ch <- ssePayload:
		default:
			// Avoid blocking on slow receivers
		}
	}
}
