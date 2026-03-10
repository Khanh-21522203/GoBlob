package shell

import (
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"GoBlob/goblob/core/types"
)

const adminShellLockName = "__admin_shell_lock__"

// ShellOption configures an admin shell session.
type ShellOption struct {
	Masters      []string
	FilerAddress string
	Directory    string
}

// CommandEnv carries shared runtime context for shell commands.
type CommandEnv struct {
	GrpcDialOption grpc.DialOption

	option *ShellOption

	lockMu         sync.Mutex
	isLocked       bool
	lockRenewToken string
	lockOwner      string
}

func newCommandEnv(opt *ShellOption, grpcDialOption grpc.DialOption) *CommandEnv {
	if opt == nil {
		opt = &ShellOption{}
	}
	dir := strings.TrimSpace(opt.Directory)
	if dir == "" {
		dir = "/"
	}
	opt.Directory = normalizePath(dir)

	return &CommandEnv{
		option:         opt,
		GrpcDialOption: grpcDialOption,
		lockOwner:      fmt.Sprintf("shell-%d", time.Now().UnixNano()),
	}
}

func (e *CommandEnv) masterGRPCAddress() string {
	if e == nil || e.option == nil || len(e.option.Masters) == 0 {
		return ""
	}
	addr := strings.TrimSpace(e.option.Masters[0])
	if addr == "" {
		return ""
	}
	return string(types.ServerAddress(addr).ToGrpcAddress())
}

func (e *CommandEnv) masterHTTPAddress() string {
	if e == nil || e.option == nil || len(e.option.Masters) == 0 {
		return ""
	}
	addr := strings.TrimSpace(e.option.Masters[0])
	if addr == "" {
		return ""
	}
	return string(types.ServerAddress(addr).ToHttpAddress())
}

func (e *CommandEnv) filerGRPCAddress() string {
	if e == nil || e.option == nil {
		return ""
	}
	addr := strings.TrimSpace(e.option.FilerAddress)
	if addr == "" {
		return ""
	}
	return string(types.ServerAddress(addr).ToGrpcAddress())
}

func (e *CommandEnv) currentDirectory() string {
	if e == nil || e.option == nil {
		return "/"
	}
	return e.option.Directory
}

func normalizePath(p string) string {
	if strings.TrimSpace(p) == "" {
		return "/"
	}
	cleaned := path.Clean("/" + p)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func resolvePath(cwd, input string) string {
	in := strings.TrimSpace(input)
	if in == "" {
		return normalizePath(cwd)
	}
	if strings.HasPrefix(in, "/") {
		return normalizePath(in)
	}
	return normalizePath(path.Join(normalizePath(cwd), in))
}
