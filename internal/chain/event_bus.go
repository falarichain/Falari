package chain

import (
	"sync"

	"chain/internal/wire"
)

// EventBus provides a publish/subscribe mechanism for chain events.
// Subscribers receive events in real-time via buffered channels.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan wire.ChainEvent]struct{}
}

// NewEventBus creates a new EventBus instance.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan wire.ChainEvent]struct{}),
	}
}

// Subscribe registers a new subscriber and returns a buffered channel
// that will receive all published events. The channel has a capacity of 256;
// if the subscriber cannot keep up, events are dropped (non-blocking send).
func (b *EventBus) Subscribe() chan wire.ChainEvent {
	ch := make(chan wire.ChainEvent, 256)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *EventBus) Unsubscribe(ch chan wire.ChainEvent) {
	b.mu.Lock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Publish sends an event to all subscribers. Uses non-blocking sends
// to prevent slow subscribers from blocking the event emission path.
func (b *EventBus) Publish(event wire.ChainEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// Subscriber channel full — drop event to avoid blocking.
		}
	}
}
