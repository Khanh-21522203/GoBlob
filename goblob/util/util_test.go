package util

import (
	"sync"
	"testing"
)

func TestConcurrentReadMap(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		m := NewConcurrentReadMap[string, int]()

		// Test Get on empty map
		_, ok := m.Get("key1")
		if ok {
			t.Error("expected false for non-existent key")
		}

		// Test Set
		m.Set("key1", 42)

		// Test Get after Set
		val, ok := m.Get("key1")
		if !ok {
			t.Error("expected true for existent key")
		}
		if val != 42 {
			t.Errorf("expected 42, got %d", val)
		}

		// Test Update
		m.Set("key1", 100)
		val, ok = m.Get("key1")
		if !ok || val != 100 {
			t.Errorf("expected 100, got %d", val)
		}

		// Test Delete
		m.Delete("key1")
		_, ok = m.Get("key1")
		if ok {
			t.Error("expected false after delete")
		}

		// Test Len
		m.Set("a", 1)
		m.Set("b", 2)
		m.Set("c", 3)
		if m.Len() != 3 {
			t.Errorf("expected length 3, got %d", m.Len())
		}
	})

	t.Run("range", func(t *testing.T) {
		m := NewConcurrentReadMap[string, int]()
		m.Set("a", 1)
		m.Set("b", 2)
		m.Set("c", 3)

		count := 0
		sum := 0
		m.Range(func(k string, v int) bool {
			count++
			sum += v
			return true
		})

		if count != 3 {
			t.Errorf("expected 3 items, got %d", count)
		}
		if sum != 6 {
			t.Errorf("expected sum 6, got %d", sum)
		}
	})

	t.Run("range early stop", func(t *testing.T) {
		m := NewConcurrentReadMap[string, int]()
		m.Set("a", 1)
		m.Set("b", 2)
		m.Set("c", 3)

		count := 0
		m.Range(func(k string, v int) bool {
			count++
			return count < 2
		})

		if count != 2 {
			t.Errorf("expected 2 items before early stop, got %d", count)
		}
	})

	t.Run("keys", func(t *testing.T) {
		m := NewConcurrentReadMap[string, int]()
		m.Set("c", 3)
		m.Set("a", 1)
		m.Set("b", 2)

		keys := m.Keys()
		if len(keys) != 3 {
			t.Errorf("expected 3 keys, got %d", len(keys))
		}
	})
}

func TestConcurrentReadMapConcurrent(t *testing.T) {
	m := NewConcurrentReadMap[string, int]()
	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := string(rune('a' + i))
				m.Set(key, j)
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := string(rune('a' + i))
				m.Get(key)
			}
		}(i)
	}

	wg.Wait()
}

func TestUnboundedQueue(t *testing.T) {
	t.Run("basic enqueue/dequeue", func(t *testing.T) {
		q := NewUnboundedQueue[int]()

		q.Enqueue(1)
		q.Enqueue(2)
		q.Enqueue(3)

		if q.Dequeue() != 1 {
			t.Error("expected 1")
		}
		if q.Dequeue() != 2 {
			t.Error("expected 2")
		}
		if q.Dequeue() != 3 {
			t.Error("expected 3")
		}
	})

	t.Run("try dequeue empty", func(t *testing.T) {
		q := NewUnboundedQueue[int]()

		val, ok := q.TryDequeue()
		if ok {
			t.Error("expected false for empty queue")
		}
		if val != 0 {
			t.Errorf("expected zero value, got %d", val)
		}
	})

	t.Run("try dequeue with items", func(t *testing.T) {
		q := NewUnboundedQueue[int]()
		q.Enqueue(42)

		val, ok := q.TryDequeue()
		if !ok {
			t.Error("expected true for non-empty queue")
		}
		if val != 42 {
			t.Errorf("expected 42, got %d", val)
		}
	})

	t.Run("FIFO order", func(t *testing.T) {
		q := NewUnboundedQueue[int]()
		q.Enqueue(1)
		q.Enqueue(2)
		q.Enqueue(3)

		val1 := q.Dequeue()
		val2 := q.Dequeue()
		val3 := q.Dequeue()

		if val1 != 1 || val2 != 2 || val3 != 3 {
			t.Errorf("expected [1,2,3], got [%d,%d,%d]", val1, val2, val3)
		}
	})
}

func TestUnboundedQueueConcurrent(t *testing.T) {
	q := NewUnboundedQueue[int]()
	var wg sync.WaitGroup

	// Enqueuers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				q.Enqueue(i*100 + j)
			}
		}(i)
	}

	// Dequeuers
	sum := 0
	var mu sync.Mutex
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				val := q.Dequeue()
				mu.Lock()
				sum += val
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	// Sum of 0..499 = 499*500/2 = 124750
	expected := 124750
	if sum != expected {
		t.Errorf("expected sum %d, got %d", expected, sum)
	}
}

func TestFullPath(t *testing.T) {
	t.Run("dir", func(t *testing.T) {
		tests := []struct {
			input    FullPath
			expected FullPath
		}{
			{"/foo/bar/baz.txt", "/foo/bar"},
			{"/foo/bar", "/foo"},
			{"/foo", "/"},
			{"/", "/"},
		}

		for _, tt := range tests {
			if got := tt.input.Dir(); got != tt.expected {
				t.Errorf("Dir(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})

	t.Run("name", func(t *testing.T) {
		tests := []struct {
			input    FullPath
			expected string
		}{
			{"/foo/bar/baz.txt", "baz.txt"},
			{"/foo/bar/", "bar"},
			{"/foo", "foo"},
			{"/", "/"},
		}

		for _, tt := range tests {
			if got := tt.input.Name(); got != tt.expected {
				t.Errorf("Name(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})

	t.Run("child", func(t *testing.T) {
		tests := []struct {
			input    FullPath
			name     string
			expected FullPath
		}{
			{"/foo", "bar", "/foo/bar"},
			{"/", "tmp", "/tmp"},
			{"/foo/", "bar", "/foo/bar"},
		}

		for _, tt := range tests {
			if got := tt.input.Child(tt.name); got != tt.expected {
				t.Errorf("Child(%q, %q) = %q, want %q", tt.input, tt.name, got, tt.expected)
			}
		}
	})

	t.Run("is root", func(t *testing.T) {
		if !FullPath("/").IsRoot() {
			t.Error("expected true for /")
		}
		if FullPath("/foo").IsRoot() {
			t.Error("expected false for /foo")
		}
	})

	t.Run("new full path", func(t *testing.T) {
		tests := []struct {
			input    string
			expected FullPath
		}{
			{"foo/bar", "/foo/bar"},
			{"/foo/bar", "/foo/bar"},
			{"", "/"},
			{"/", "/"},
		}

		for _, tt := range tests {
			if got := NewFullPath(tt.input); got != tt.expected {
				t.Errorf("NewFullPath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})
}

func TestNumericHelpers(t *testing.T) {
	t.Run("min uint64", func(t *testing.T) {
		if MinUint64(1, 2) != 1 {
			t.Error("expected 1")
		}
		if MinUint64(2, 1) != 1 {
			t.Error("expected 1")
		}
		if MinUint64(5, 5) != 5 {
			t.Error("expected 5")
		}
	})

	t.Run("max uint64", func(t *testing.T) {
		if MaxUint64(1, 2) != 2 {
			t.Error("expected 2")
		}
		if MaxUint64(2, 1) != 2 {
			t.Error("expected 2")
		}
		if MaxUint64(5, 5) != 5 {
			t.Error("expected 5")
		}
	})

	t.Run("min int", func(t *testing.T) {
		if MinInt(1, 2) != 1 {
			t.Error("expected 1")
		}
		if MinInt(-1, 1) != -1 {
			t.Error("expected -1")
		}
	})

	t.Run("max int", func(t *testing.T) {
		if MaxInt(1, 2) != 2 {
			t.Error("expected 2")
		}
		if MaxInt(-1, 1) != 1 {
			t.Error("expected 1")
		}
	})

	t.Run("min int32", func(t *testing.T) {
		if MinInt32(1, 2) != 1 {
			t.Error("expected 1")
		}
		if MinInt32(-1, 1) != -1 {
			t.Error("expected -1")
		}
	})

	t.Run("max int32", func(t *testing.T) {
		if MaxInt32(1, 2) != 2 {
			t.Error("expected 2")
		}
		if MaxInt32(-1, 1) != 1 {
			t.Error("expected 1")
		}
	})
}
