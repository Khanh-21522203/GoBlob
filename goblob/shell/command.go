package shell

import (
	"io"
	"sort"
	"strings"
	"sync"
)

// Command is the admin shell command contract.
type Command interface {
	Name() string
	Help() string
	Do(args []string, env *CommandEnv, writer io.Writer) error
}

var (
	commandsMu sync.RWMutex
	commands   []Command
)

// RegisterCommand adds a shell command to the global registry.
func RegisterCommand(cmd Command) {
	if cmd == nil || strings.TrimSpace(cmd.Name()) == "" {
		return
	}
	commandsMu.Lock()
	defer commandsMu.Unlock()
	commands = append(commands, cmd)
}

// RegisteredCommands returns all globally registered commands sorted by name.
func RegisteredCommands() []Command {
	commandsMu.RLock()
	defer commandsMu.RUnlock()
	out := make([]Command, len(commands))
	copy(out, commands)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
