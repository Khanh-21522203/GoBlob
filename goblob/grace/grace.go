package grace

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	hooks       []func()
	mu          sync.Mutex
	notified    bool
	notifyCount int
)

// resetForTest resets the package state for testing.
// This should NOT be used in production code.
func resetForTest() {
	mu.Lock()
	defer mu.Unlock()
	hooks = nil
	notified = false
	notifyCount = 0
}

// OnInterrupt registers fn to be called on SIGINT or SIGTERM.
// Hooks are called in LIFO order (last registered, first called).
func OnInterrupt(fn func()) {
	mu.Lock()
	defer mu.Unlock()
	hooks = append(hooks, fn)
}

// init starts the signal listener goroutine once.
func init() {
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		runHooks()
		os.Exit(0)
	}()
}

// runHooks executes all registered hooks in LIFO order.
// Hooks registered after this call will NOT be included.
func runHooks() {
	mu.Lock()
	// Take a snapshot of current hooks
	h := make([]func(), len(hooks))
	copy(h, hooks)
	// Mark that we've notified
	notified = true
	notifyCount++
	mu.Unlock()

	// Call in LIFO order (last registered, first called)
	for i := len(h) - 1; i >= 0; i-- {
		h[i]()
	}
}

// NotifyExit triggers shutdown programmatically (for tests and controlled shutdown).
// This does NOT call os.Exit - only the signal handler does.
// Hooks only run once; subsequent calls are no-ops.
func NotifyExit() {
	mu.Lock()
	alreadyNotified := notified
	mu.Unlock()

	if !alreadyNotified {
		runHooks()
	}
}
