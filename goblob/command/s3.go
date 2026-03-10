package command

import (
	"context"
	"flag"
	"net/http"
	"strings"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/s3api"
)

type S3Command struct {
	host        string
	port        int
	filers      string
	domainName  string
	bucketsPath string
}

func init() {
	Register(&S3Command{})
}

func (c *S3Command) Name() string     { return "s3" }
func (c *S3Command) Synopsis() string { return "run S3 gateway" }
func (c *S3Command) Usage() string {
	return "blob s3 -filer localhost:8888 -port 8333"
}

func (c *S3Command) SetFlags(fs *flag.FlagSet) {
	def := s3api.DefaultS3ApiServerOption()
	fs.StringVar(&c.host, "ip", "127.0.0.1", "bind host")
	fs.IntVar(&c.port, "port", def.Port, "s3 HTTP port")
	fs.StringVar(&c.filers, "filer", "127.0.0.1:8888", "comma-separated filer HTTP addresses")
	fs.StringVar(&c.domainName, "domainName", "", "s3 domain name")
	fs.StringVar(&c.bucketsPath, "filer.path", def.BucketsPath, "buckets path in filer")
}

func (c *S3Command) Run(ctx context.Context, args []string) error {
	_ = args
	filerAddrs := splitCSV(c.filers)
	filers := make([]types.ServerAddress, 0, len(filerAddrs))
	for _, addr := range filerAddrs {
		filers = append(filers, types.ServerAddress(strings.TrimSpace(addr)))
	}
	opt := s3api.DefaultS3ApiServerOption()
	opt.Filers = filers
	opt.Port = c.port
	opt.DomainName = c.domainName
	opt.BucketsPath = c.bucketsPath

	mux := http.NewServeMux()
	s3, err := s3api.NewS3ApiServer(mux, opt)
	if err != nil {
		return err
	}

	rt, err := startHTTPRuntime(c.host, c.port, mux, s3.Shutdown)
	if err != nil {
		return err
	}
	reload := func() error {
		return s3.ReloadIAM(context.Background())
	}
	if err := reload(); err != nil {
		shutdownCtx, cancel := shutdownCtx()
		rt.shutdown(shutdownCtx)
		cancel()
		return err
	}
	setReloadHook(reload)
	defer setReloadHook(nil)

	<-ctx.Done()
	shutdownCtx, cancel := shutdownCtx()
	defer cancel()
	rt.shutdown(shutdownCtx)
	return nil
}
