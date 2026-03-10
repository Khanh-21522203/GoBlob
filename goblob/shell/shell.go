package shell

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Shell is an interactive admin shell.
type Shell struct {
	env      *CommandEnv
	commands map[string]Command
	writer   io.Writer
	reader   io.Reader
}

// NewShell creates a shell from options and an outbound gRPC dial option.
func NewShell(opt *ShellOption, grpcDialOption grpc.DialOption, reader io.Reader, writer io.Writer) (*Shell, error) {
	if reader == nil {
		return nil, fmt.Errorf("reader is required")
	}
	if writer == nil {
		return nil, fmt.Errorf("writer is required")
	}
	if grpcDialOption == nil {
		grpcDialOption = grpc.WithTransportCredentials(insecure.NewCredentials())
	}
	s := &Shell{
		env:      newCommandEnv(opt, grpcDialOption),
		commands: make(map[string]Command),
		writer:   writer,
		reader:   reader,
	}
	for _, cmd := range RegisteredCommands() {
		s.RegisterCommand(cmd)
	}
	return s, nil
}

// RegisterCommand registers a command into this shell's runtime registry.
func (s *Shell) RegisterCommand(cmd Command) {
	if s == nil || cmd == nil || strings.TrimSpace(cmd.Name()) == "" {
		return
	}
	if s.commands == nil {
		s.commands = make(map[string]Command)
	}
	s.commands[cmd.Name()] = cmd
}

// Run starts an interactive REPL.
func (s *Shell) Run() error {
	if s == nil {
		return fmt.Errorf("shell is nil")
	}
	br := bufio.NewReader(s.reader)
	for {
		if _, err := fmt.Fprint(s.writer, "goblob> "); err != nil {
			return err
		}
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				return nil
			}
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}
		if execErr := s.Exec(line); execErr != nil {
			if _, werr := fmt.Fprintf(s.writer, "error: %v\n", execErr); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

// RunScript executes a newline-delimited script.
func (s *Shell) RunScript(script string) error {
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if err := s.Exec(trimmed); err != nil {
			return err
		}
	}
	return nil
}

// Exec executes a single command line.
func (s *Shell) Exec(line string) error {
	if s == nil {
		return fmt.Errorf("shell is nil")
	}
	args := shellSplit(line)
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "help":
		s.printHelp()
		return nil
	case "exit", "quit":
		return nil
	}

	cmd, ok := s.commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	return cmd.Do(args, s.env, s.writer)
}

func (s *Shell) printHelp() {
	names := make([]string, 0, len(s.commands))
	for name := range s.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(s.writer, "%-16s %s\n", name, s.commands[name].Help())
	}
}

func shellSplit(line string) []string {
	var (
		out     []string
		current strings.Builder
		quote   rune
		escape  bool
	)

	flush := func() {
		if current.Len() == 0 {
			return
		}
		out = append(out, current.String())
		current.Reset()
	}

	for _, r := range line {
		if escape {
			current.WriteRune(r)
			escape = false
			continue
		}

		if r == '\\' {
			escape = true
			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}

		switch r {
		case '"', '\'':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}

	if escape {
		current.WriteRune('\\')
	}
	flush()
	return out
}
