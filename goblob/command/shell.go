package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"GoBlob/goblob/shell"
)

type ShellCommand struct {
	masters string
	filer   string
	once    string
}

func init() {
	Register(&ShellCommand{})
}

func (c *ShellCommand) Name() string     { return "shell" }
func (c *ShellCommand) Synopsis() string { return "run admin shell" }
func (c *ShellCommand) Usage() string {
	return "blob shell -masters localhost:9333 -filer localhost:8888"
}

func (c *ShellCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.masters, "masters", "127.0.0.1:9333", "comma-separated master HTTP addresses")
	fs.StringVar(&c.filer, "filer", "127.0.0.1:8888", "filer HTTP address")
	fs.StringVar(&c.once, "c", "", "execute one command and exit")
}

func (c *ShellCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args
	opt := &shell.ShellOption{
		Masters:      splitCSV(c.masters),
		FilerAddress: strings.TrimSpace(c.filer),
		Directory:    "/",
	}
	sh, err := shell.NewShell(opt, grpc.WithTransportCredentials(insecure.NewCredentials()), os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	if c.once != "" {
		return sh.Exec(c.once)
	}
	if err := sh.Run(); err != nil {
		return fmt.Errorf("shell run: %w", err)
	}
	return nil
}
