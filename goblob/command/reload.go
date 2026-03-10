package command

import (
	"fmt"
	"io"
	"sync"

	"GoBlob/goblob/config"
)

var (
	reloadMu   sync.RWMutex
	reloadHook func() error
)

func setReloadHook(hook func() error) {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	reloadHook = hook
}

// HandleSIGHUP runs command-specific reload logic for a SIGHUP signal.
func HandleSIGHUP(stderr io.Writer) {
	reloadMu.RLock()
	hook := reloadHook
	reloadMu.RUnlock()

	if hook == nil {
		_, _ = fmt.Fprintln(stderr, "received SIGHUP: no reload action for current command")
		return
	}
	if err := hook(); err != nil {
		_, _ = fmt.Fprintf(stderr, "received SIGHUP: reload failed: %v\n", err)
		return
	}
	_, _ = fmt.Fprintln(stderr, "received SIGHUP: configuration reloaded")
}

func loadSecurityConfig() (*config.SecurityConfig, error) {
	loader := config.NewViperLoader(nil)
	return loader.LoadSecurityConfig()
}
