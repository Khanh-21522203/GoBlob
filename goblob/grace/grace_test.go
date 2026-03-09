package grace

import (
	"testing"
)

func TestHooksRunInLIFOOrder(t *testing.T) {
	resetForTest()
	var order []int

	OnInterrupt(func() { order = append(order, 1) })
	OnInterrupt(func() { order = append(order, 2) })
	OnInterrupt(func() { order = append(order, 3) })

	NotifyExit()

	expected := []int{3, 2, 1}
	if len(order) != len(expected) {
		t.Fatalf("expected %d hooks, got %d", len(expected), len(order))
	}

	for i, v := range expected {
		if order[i] != v {
			t.Errorf("expected order[%d] = %d, got %d", i, v, order[i])
		}
	}
}

func TestEmptyHooks(t *testing.T) {
	resetForTest()
	// Should not panic
	NotifyExit()
}

func TestSingleHook(t *testing.T) {
	resetForTest()
	called := false
	OnInterrupt(func() { called = true })

	NotifyExit()

	if !called {
		t.Error("hook was not called")
	}
}

func TestMultipleNotifyExit(t *testing.T) {
	resetForTest()
	count := 0
	OnInterrupt(func() { count++ })

	NotifyExit()
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}

	// Subsequent NotifyExit calls should be no-ops
	NotifyExit()
	if count != 1 {
		t.Errorf("expected count 1 (hooks only run once), got %d", count)
	}
}

func TestHookPanic(t *testing.T) {
	resetForTest()
	// If a hook panics, it should propagate
	OnInterrupt(func() {
		panic("test panic")
	})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic to propagate")
		}
	}()

	NotifyExit()
}
