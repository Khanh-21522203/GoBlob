package command

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"
)

type testCommand struct {
	name    string
	flagVal string
	args    []string
	runErr  error
}

func (c *testCommand) Name() string     { return c.name }
func (c *testCommand) Synopsis() string { return "test command" }
func (c *testCommand) Usage() string    { return "test command usage" }
func (c *testCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.flagVal, "name", "", "name flag")
}
func (c *testCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	c.args = append([]string(nil), args...)
	return c.runErr
}

func TestExecuteUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"no.such.command"}, nil, &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got: %s", stderr.String())
	}
}

func TestExecuteDispatchesCommandWithFlags(t *testing.T) {
	cmd := &testCommand{name: "test.exec"}
	Register(cmd)

	var stderr bytes.Buffer
	code := Execute(
		context.Background(),
		[]string{"test.exec", "-name", "alice", "arg1", "arg2"},
		nil,
		&bytes.Buffer{},
		&stderr,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, stderr.String())
	}
	if cmd.flagVal != "alice" {
		t.Fatalf("expected flag value alice, got %q", cmd.flagVal)
	}
	if len(cmd.args) != 2 || cmd.args[0] != "arg1" || cmd.args[1] != "arg2" {
		t.Fatalf("unexpected args: %#v", cmd.args)
	}
}

func TestExecuteHelpAndVersion(t *testing.T) {
	var helpOut bytes.Buffer
	helpCode := Execute(context.Background(), []string{"help"}, nil, &helpOut, &bytes.Buffer{})
	if helpCode != 0 {
		t.Fatalf("expected help exit code 0, got %d", helpCode)
	}
	if !strings.Contains(helpOut.String(), "Usage:") {
		t.Fatalf("help output missing usage: %s", helpOut.String())
	}

	var versionOut bytes.Buffer
	versionCode := Execute(context.Background(), []string{"version"}, nil, &versionOut, &bytes.Buffer{})
	if versionCode != 0 {
		t.Fatalf("expected version exit code 0, got %d", versionCode)
	}
	if !strings.Contains(versionOut.String(), "GoBlob") {
		t.Fatalf("version output missing marker: %s", versionOut.String())
	}
}
