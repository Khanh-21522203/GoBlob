package command

import (
	"context"
	"flag"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"GoBlob/goblob/shell"
)

type FixCommand struct {
	masters string
	filer   string
	cmd     string
}

func init() {
	Register(&FixCommand{})
}

func (c *FixCommand) Name() string     { return "fix" }
func (c *FixCommand) Synopsis() string { return "run a maintenance fix command via admin shell" }
func (c *FixCommand) Usage() string {
	return "blob fix -cmd \"volume.fix.replication -dryRun=true\""
}

func (c *FixCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.masters, "masters", "127.0.0.1:9333", "comma-separated master HTTP addresses")
	fs.StringVar(&c.filer, "filer", "127.0.0.1:8888", "filer HTTP address")
	fs.StringVar(&c.cmd, "cmd", "volume.fix.replication -dryRun=true", "shell command to execute")
}

func (c *FixCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args
	opt := &shell.ShellOption{
		Masters:      splitCSV(c.masters),
		FilerAddress: strings.TrimSpace(c.filer),
		Directory:    "/",
	}
	sh, err := shell.NewShell(opt, grpc.WithTransportCredentials(insecure.NewCredentials()), strings.NewReader(""), os.Stdout)
	if err != nil {
		return err
	}
	return sh.Exec(c.cmd)
}
