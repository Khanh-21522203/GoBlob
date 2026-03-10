package command

import (
	"fmt"
	"io"
	"runtime"
)

var (
	// These values can be overridden at build time with -ldflags.
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func printVersion(w io.Writer) {
	_, _ = fmt.Fprintf(w, "GoBlob %s\n", Version)
	_, _ = fmt.Fprintf(w, "commit: %s\n", Commit)
	_, _ = fmt.Fprintf(w, "build date: %s\n", BuildDate)
	_, _ = fmt.Fprintf(w, "go: %s\n", runtime.Version())
}
