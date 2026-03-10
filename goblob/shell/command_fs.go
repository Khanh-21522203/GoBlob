package shell

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/filer_pb"
)

type fsLsCommand struct{}

func init() {
	RegisterCommand(&fsLsCommand{})
}

func (c *fsLsCommand) Name() string { return "fs.ls" }

func (c *fsLsCommand) Help() string {
	return "list filer directory entries: fs.ls [path]"
}

func (c *fsLsCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	filerAddr := env.filerGRPCAddress()
	if filerAddr == "" {
		return fmt.Errorf("no filer address configured")
	}

	target := env.currentDirectory()
	if len(args) > 1 {
		target = resolvePath(env.currentDirectory(), args[1])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type row struct {
		name  string
		size  uint64
		isDir bool
	}
	rows := make([]row, 0, 32)

	err := pb.WithFilerClient(filerAddr, env.GrpcDialOption, func(client filer_pb.FilerServiceClient) error {
		stream, err := client.ListEntries(ctx, &filer_pb.ListEntriesRequest{
			Directory: target,
			Limit:     10000,
		})
		if err != nil {
			return err
		}
		for {
			resp, recvErr := stream.Recv()
			if recvErr == io.EOF {
				return nil
			}
			if recvErr != nil {
				return recvErr
			}
			entry := resp.GetEntry()
			if entry == nil {
				continue
			}
			name := entry.GetName()
			if name == "" {
				continue
			}
			size := uint64(0)
			if attr := entry.GetAttributes(); attr != nil {
				size = attr.GetFileSize()
			}
			rows = append(rows, row{name: name, size: size, isDir: entry.GetIsDirectory()})
		}
	})
	if err != nil {
		return fmt.Errorf("list %s: %w", target, err)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	_, _ = fmt.Fprintf(writer, "%s\n", target)
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(writer, "(empty)")
		return nil
	}
	for _, r := range rows {
		name := r.name
		if r.isDir {
			name += "/"
		}
		_, _ = fmt.Fprintf(writer, "%s\t%d\n", name, r.size)
	}
	return nil
}
