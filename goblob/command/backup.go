package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/filer_pb"
)

type BackupCommand struct {
	filer string
	root  string
	out   string
}

func init() {
	Register(&BackupCommand{})
}

func (c *BackupCommand) Name() string { return "backup" }
func (c *BackupCommand) Synopsis() string {
	return "export filer metadata entries into a JSON backup file"
}
func (c *BackupCommand) Usage() string {
	return "blob backup -filer 127.0.0.1:8888 -path / -out backup.json"
}

func (c *BackupCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.filer, "filer", "127.0.0.1:8888", "filer HTTP address")
	fs.StringVar(&c.root, "path", "/", "filer root path to backup")
	fs.StringVar(&c.out, "out", "backup.json", "output JSON file, use - for stdout")
}

type backupEntry struct {
	Path        string `json:"path"`
	IsDirectory bool   `json:"is_directory"`
	Size        uint64 `json:"size"`
	Mtime       int64  `json:"mtime"`
}

type backupDocument struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Root        string        `json:"root"`
	Entries     []backupEntry `json:"entries"`
}

func (c *BackupCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args
	filerAddr := string(types.ServerAddress(c.filer).ToGrpcAddress())
	if filerAddr == "" {
		return fmt.Errorf("empty filer address")
	}
	root := path.Clean("/" + strings.TrimSpace(c.root))
	entries := make([]backupEntry, 0, 128)
	visited := make(map[string]struct{})
	if err := c.collect(root, filerAddr, visited, &entries); err != nil {
		return err
	}

	doc := backupDocument{
		GeneratedAt: time.Now().UTC(),
		Root:        root,
		Entries:     entries,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if c.out == "-" {
		_, _ = os.Stdout.Write(data)
		_, _ = os.Stdout.Write([]byte{'\n'})
		return nil
	}
	return os.WriteFile(c.out, data, 0644)
}

func (c *BackupCommand) collect(dir, filerAddr string, visited map[string]struct{}, out *[]backupEntry) error {
	if _, ok := visited[dir]; ok {
		return nil
	}
	visited[dir] = struct{}{}

	return pb.WithFilerClient(filerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), func(client filer_pb.FilerServiceClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		stream, err := client.ListEntries(ctx, &filer_pb.ListEntriesRequest{
			Directory: dir,
			Limit:     10000,
		})
		if err != nil {
			return err
		}

		for {
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr == io.EOF {
					return nil
				}
				return recvErr
			}
			entry := resp.GetEntry()
			if entry == nil || entry.GetName() == "" {
				continue
			}
			fullPath := path.Join(dir, entry.GetName())
			size := uint64(0)
			mtime := int64(0)
			if attrs := entry.GetAttributes(); attrs != nil {
				size = attrs.GetFileSize()
				mtime = attrs.GetMtime()
			}
			*out = append(*out, backupEntry{
				Path:        fullPath,
				IsDirectory: entry.GetIsDirectory(),
				Size:        size,
				Mtime:       mtime,
			})
			if entry.GetIsDirectory() {
				if err := c.collect(fullPath, filerAddr, visited, out); err != nil {
					return err
				}
			}
		}
	})
}
