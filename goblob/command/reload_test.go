package command

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandleSIGHUPNoHook(t *testing.T) {
	setReloadHook(nil)

	var stderr bytes.Buffer
	HandleSIGHUP(&stderr)
	if !strings.Contains(stderr.String(), "no reload action") {
		t.Fatalf("expected no reload action message, got: %q", stderr.String())
	}
}

func TestHandleSIGHUPWithHook(t *testing.T) {
	defer setReloadHook(nil)

	called := false
	setReloadHook(func() error {
		called = true
		return nil
	})

	var stderr bytes.Buffer
	HandleSIGHUP(&stderr)
	if !called {
		t.Fatalf("expected reload hook to be called")
	}
	if !strings.Contains(stderr.String(), "configuration reloaded") {
		t.Fatalf("expected successful reload message, got: %q", stderr.String())
	}
}

func TestHandleSIGHUPWithHookError(t *testing.T) {
	defer setReloadHook(nil)

	setReloadHook(func() error {
		return errTestReload
	})

	var stderr bytes.Buffer
	HandleSIGHUP(&stderr)
	if !strings.Contains(stderr.String(), "reload failed") {
		t.Fatalf("expected reload failed message, got: %q", stderr.String())
	}
}

var errTestReload = &testReloadError{msg: "boom"}

type testReloadError struct {
	msg string
}

func (e *testReloadError) Error() string { return e.msg }
