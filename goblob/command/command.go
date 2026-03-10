package command

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Command is the CLI subcommand contract.
type Command interface {
	Name() string
	Synopsis() string
	Usage() string
	SetFlags(fs *flag.FlagSet)
	Run(ctx context.Context, args []string) error
}

var (
	commandsMu sync.RWMutex
	commands   = make(map[string]Command)
)

// Register registers a command. Re-registering a name overwrites the previous command.
func Register(cmd Command) {
	if cmd == nil {
		return
	}
	name := strings.TrimSpace(cmd.Name())
	if name == "" {
		return
	}
	commandsMu.Lock()
	defer commandsMu.Unlock()
	commands[name] = cmd
}

// Lookup returns a command by name.
func Lookup(name string) Command {
	commandsMu.RLock()
	defer commandsMu.RUnlock()
	return commands[name]
}

// List returns all registered commands sorted by name.
func List() []Command {
	commandsMu.RLock()
	defer commandsMu.RUnlock()
	out := make([]Command, 0, len(commands))
	for _, cmd := range commands {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Execute parses argv (without program name), dispatches to a command, and returns exit code.
func Execute(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	_ = stdin
	setReloadHook(nil)
	if len(argv) == 0 {
		printHelp(stdout)
		return 2
	}

	name := strings.TrimSpace(argv[0])
	switch name {
	case "help", "-h", "--help":
		printHelp(stdout)
		return 0
	case "version", "--version", "-version":
		printVersion(stdout)
		return 0
	}

	cmd := Lookup(name)
	if cmd == nil {
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n", name)
		_, _ = fmt.Fprintln(stderr, "run 'blob help' for available commands")
		return 2
	}

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cmd.SetFlags(fs)
	if err := fs.Parse(argv[1:]); err != nil {
		return 2
	}

	if err := cmd.Run(ctx, fs.Args()); err != nil {
		_, _ = fmt.Fprintf(stderr, "command %q failed: %v\n", name, err)
		return 1
	}
	return 0
}

func printHelp(w io.Writer) {
	_, _ = fmt.Fprintln(w, "GoBlob command line interface")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  blob <command> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Commands:")
	for _, cmd := range List() {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", cmd.Name(), cmd.Synopsis())
	}
	_, _ = fmt.Fprintln(w, "  help       show this help")
	_, _ = fmt.Fprintln(w, "  version    show build version")
}
