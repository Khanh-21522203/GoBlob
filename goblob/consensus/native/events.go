package native

import (
	"sync"

	"GoBlob/goblob/consensus"
)

type eventBus struct {
	mu          sync.RWMutex
	subscribers map[string]chan consensus.Event
}

func newEventBus() eventBus {
	return eventBus{subscribers: make(map[string]chan consensus.Event)}
}

func (b *eventBus) Subscribe(name string, bufSize int) <-chan consensus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.subscribers[name]; ok {
		close(old)
	}
	ch := make(chan consensus.Event, bufSize)
	b.subscribers[name] = ch
	return ch
}

func (b *eventBus) Unsubscribe(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[name]; ok {
		close(ch)
		delete(b.subscribers, name)
	}
}

func (b *eventBus) publish(e consensus.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}
