package util

import "sync"

// UnboundedQueue is a goroutine-safe, unbounded FIFO queue backed by a linked list.
// Used by the Filer for background blob deletion.
type UnboundedQueue[T any] struct {
	mu   sync.Mutex
	head *node[T]
	tail *node[T]
	cond *sync.Cond
}

type node[T any] struct {
	val  T
	next *node[T]
}

// NewUnboundedQueue creates a new empty UnboundedQueue.
func NewUnboundedQueue[T any]() *UnboundedQueue[T] {
	// Use a single dummy/sentinel node for both head and tail
	dummy := &node[T]{}
	q := &UnboundedQueue[T]{
		head: dummy,
		tail: dummy,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue adds a value to the queue.
func (q *UnboundedQueue[T]) Enqueue(v T) {
	q.mu.Lock()
	defer q.mu.Unlock()

	newNode := &node[T]{val: v}
	q.tail.next = newNode
	q.tail = newNode
	q.cond.Signal()
}

// Dequeue blocks until an item is available and returns it.
func (q *UnboundedQueue[T]) Dequeue() T {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Wait for at least one item (head.next != nil means there's an item)
	for q.head.next == nil {
		q.cond.Wait()
	}

	// Get the first real node
	firstNode := q.head.next
	val := firstNode.val

	// Move head to the next node
	q.head.next = firstNode.next

	// If we just removed the last node, reset tail to head
	if q.head.next == nil {
		q.tail = q.head
	}

	return val
}

// TryDequeue returns (value, true) if an item is ready, else (zero, false).
func (q *UnboundedQueue[T]) TryDequeue() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.head.next == nil {
		var zero T
		return zero, false
	}

	firstNode := q.head.next
	val := firstNode.val

	q.head.next = firstNode.next

	if q.head.next == nil {
		q.tail = q.head
	}

	return val, true
}

// Len returns the number of items in the queue.
// Note: This is O(n) and should only be used for debugging.
func (q *UnboundedQueue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	count := 0
	for n := q.head.next; n != nil; n = n.next {
		count++
	}
	return count
}
