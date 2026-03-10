package command

import (
	"context"
	"flag"
	"net/http"
	"strings"

	"GoBlob/goblob/security"
	webdavsrv "GoBlob/goblob/webdav"
)

// WebDAVCommand starts the WebDAV interface server.
type WebDAVCommand struct {
	host     string
	port     int
	rootDir  string
	filer    string
	filerDir string
	username string
	password string
}

func init() {
	Register(&WebDAVCommand{})
}

func (c *WebDAVCommand) Name() string     { return "webdav" }
func (c *WebDAVCommand) Synopsis() string { return "run WebDAV server" }
func (c *WebDAVCommand) Usage() string {
	return "blob webdav -ip 127.0.0.1 -port 4333 [-filer 127.0.0.1:8888 -filer.path /webdav] [-dir ./tmp/webdav]"
}

func (c *WebDAVCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.host, "ip", "127.0.0.1", "bind host")
	fs.IntVar(&c.port, "port", 4333, "webdav port")
	fs.StringVar(&c.rootDir, "dir", "./tmp/webdav", "local root directory")
	fs.StringVar(&c.filer, "filer", "", "filer address (when set, WebDAV uses filer gRPC backend)")
	fs.StringVar(&c.filerDir, "filer.path", "/webdav", "filer root path for WebDAV mode")
	fs.StringVar(&c.username, "username", "", "basic auth username (optional)")
	fs.StringVar(&c.password, "password", "", "basic auth password (required when username is set)")
}

func (c *WebDAVCommand) Run(ctx context.Context, args []string) error {
	_ = args
	var (
		fs  *webdavsrv.FilerFileSystem
		err error
	)
	if strings.TrimSpace(c.filer) != "" {
		fs, err = webdavsrv.NewFilerFileSystemFromAddress(c.filer, c.filerDir)
		if err != nil {
			return err
		}
		defer fs.Close()
	} else {
		fs = webdavsrv.NewFilerFileSystem(c.rootDir)
	}
	harden := func(h http.Handler) http.Handler {
		return security.ApplyHardening(h, security.HardeningOption{
			RatePerSecond: 200,
			Burst:         50,
			MaxBodyBytes:  256 << 20,
			Logger:        nil,
		})
	}
	server := webdavsrv.NewServer(webdavsrv.Address(c.host, c.port), fs, c.username, c.password, harden)
	return server.Start(ctx)
}
