package util

import "sync"

// ConcurrentReadMap is a thread-safe map optimized for concurrent reads.
// Write operations take an exclusive lock; reads take a shared lock.
type ConcurrentReadMap[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

// NewConcurrentReadMap creates a new empty ConcurrentReadMap.
func NewConcurrentReadMap[K comparable, V any]() *ConcurrentReadMap[K, V] {
	return &ConcurrentReadMap[K, V]{
		m: make(map[K]V),
	}
}

// Get returns the value for the given key and a boolean indicating if the key was found.
func (m *ConcurrentReadMap[K, V]) Get(key K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.m[key]
	return val, ok
}

// Set sets the value for the given key.
func (m *ConcurrentReadMap[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[key] = value
}

// Delete removes the value for the given key.
func (m *ConcurrentReadMap[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
}

// Range calls fn for each entry in the map.
// If fn returns false, iteration stops.
func (m *ConcurrentReadMap[K, V]) Range(fn func(K, V) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, v := range m.m {
		if !fn(k, v) {
			return
		}
	}
}

// Len returns the number of entries in the map.
func (m *ConcurrentReadMap[K, V]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.m)
}

// Keys returns a slice of all keys in the map.
func (m *ConcurrentReadMap[K, V]) Keys() []K {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]K, 0, len(m.m))
	for k := range m.m {
		keys = append(keys, k)
	}
	return keys
}
