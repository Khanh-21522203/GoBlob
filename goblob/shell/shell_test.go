package shell

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

type fakeCommand struct {
	name string
	args []string
}

func (c *fakeCommand) Name() string { return c.name }

func (c *fakeCommand) Help() string { return "fake command" }

func (c *fakeCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	_ = env
	_ = writer
	c.args = append([]string(nil), args...)
	return nil
}

func TestShellSplit(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{line: `fs.ls /`, want: []string{"fs.ls", "/"}},
		{line: `fs.ls "/tmp/data set"`, want: []string{"fs.ls", "/tmp/data set"}},
		{line: `cmd 'quoted value' tail`, want: []string{"cmd", "quoted value", "tail"}},
		{line: `cmd /tmp\ path`, want: []string{"cmd", "/tmp path"}},
		{line: `  lock   `, want: []string{"lock"}},
		{line: `a "" b`, want: []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := shellSplit(tc.line)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("shellSplit(%q)=%#v want %#v", tc.line, got, tc.want)
		}
	}
}

func TestNewShellRegistersBuiltins(t *testing.T) {
	sh, err := NewShell(&ShellOption{
		Masters:      []string{"127.0.0.1:9333"},
		FilerAddress: "127.0.0.1:8888",
	}, nil, strings.NewReader(""), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewShell returned error: %v", err)
	}
	if _, ok := sh.commands["cluster.status"]; !ok {
		t.Fatalf("expected cluster.status command to be registered")
	}
	if _, ok := sh.commands["volume.list"]; !ok {
		t.Fatalf("expected volume.list command to be registered")
	}
	if _, ok := sh.commands["fs.ls"]; !ok {
		t.Fatalf("expected fs.ls command to be registered")
	}
	if _, ok := sh.commands["volume.balance"]; !ok {
		t.Fatalf("expected volume.balance command to be registered")
	}
	if _, ok := sh.commands["s3.clean.uploads"]; !ok {
		t.Fatalf("expected s3.clean.uploads command to be registered")
	}
}

func TestExecDispatchesRegisteredCommand(t *testing.T) {
	sh, err := NewShell(&ShellOption{}, nil, strings.NewReader(""), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewShell returned error: %v", err)
	}
	fake := &fakeCommand{name: "__test.exec__"}
	sh.RegisterCommand(fake)

	if err := sh.Exec(`__test.exec__ "hello world" tail`); err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	want := []string{"__test.exec__", "hello world", "tail"}
	if !reflect.DeepEqual(fake.args, want) {
		t.Fatalf("unexpected command args: %#v want %#v", fake.args, want)
	}
}

func TestExecUnknownCommand(t *testing.T) {
	sh, err := NewShell(&ShellOption{}, nil, strings.NewReader(""), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewShell returned error: %v", err)
	}
	if err := sh.Exec("does.not.exist"); err == nil {
		t.Fatalf("expected unknown command error")
	}
}
