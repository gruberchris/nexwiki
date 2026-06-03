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
}

// NewEventBus builds a thread-safe pub-sub manager.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan string]bool),
		buffer:      make([]LogEvent, 0, 200),
		bufferLimit: 200,
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
	eb.mu.Unlock()

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
