package raft

import "sync"

// EventKind identifies the type of replicated master-state change.
type EventKind uint8

const (
	// EventMaxFileId is published when the max file ID advances.
	EventMaxFileId EventKind = iota
	// EventMaxVolumeId is published when the max volume ID advances.
	EventMaxVolumeId
	// EventTopologyId is published when the cluster topology ID is set.
	EventTopologyId
	// EventRestored is published after FSM.Restore completes.
	// All three scalar fields are valid.
	EventRestored
)

// StateEvent is an immutable description of a single replicated transition.
// All fields are value types so subscribers cannot mutate shared state.
type StateEvent struct {
	Kind        EventKind
	MaxFileId   uint64 // valid for EventMaxFileId and EventRestored
	MaxVolumeId uint32 // valid for EventMaxVolumeId and EventRestored
	TopologyId  string // valid for EventTopologyId and EventRestored
}

// EventBus is a thread-safe fan-out dispatcher for StateEvents.
// Embed it in any type that needs to publish events.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string]chan StateEvent
}

func newEventBus() EventBus {
	return EventBus{subscribers: make(map[string]chan StateEvent)}
}

// Subscribe registers a named observer and returns its receive channel.
// Calling Subscribe with the same name replaces the existing subscription.
// bufSize controls the channel buffer; a full channel causes events to be dropped
// rather than blocking the publisher.
func (b *EventBus) Subscribe(name string, bufSize int) <-chan StateEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.subscribers[name]; ok {
		close(old)
	}
	ch := make(chan StateEvent, bufSize)
	b.subscribers[name] = ch
	return ch
}

// Unsubscribe removes a named observer and closes its channel.
func (b *EventBus) Unsubscribe(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[name]; ok {
		close(ch)
		delete(b.subscribers, name)
	}
}

// publish fans e out to all registered subscribers.
// It never blocks: events are dropped for subscribers whose channels are full.
// Must be called without any FSM/raftServerImpl locks held.
func (b *EventBus) publish(e StateEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
			// subscriber is slow; drop rather than stall the caller
		}
	}
}
