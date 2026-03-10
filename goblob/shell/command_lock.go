package shell

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/filer_pb"
)

type lockCommand struct{}
type unlockCommand struct{}

func init() {
	RegisterCommand(&lockCommand{})
	RegisterCommand(&unlockCommand{})
}

func (c *lockCommand) Name() string { return "lock" }

func (c *lockCommand) Help() string {
	return "acquire admin shell distributed lock: lock [-expire=5m]"
}

func (c *lockCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	filerAddr := env.filerGRPCAddress()
	if filerAddr == "" {
		return fmt.Errorf("no filer address configured")
	}

	expire := 5 * time.Minute
	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "-expire=") {
			continue
		}
		d, err := time.ParseDuration(strings.TrimPrefix(arg, "-expire="))
		if err != nil {
			return fmt.Errorf("invalid expire duration: %w", err)
		}
		if d > 0 {
			expire = d
		}
	}

	env.lockMu.Lock()
	renewToken := env.lockRenewToken
	owner := env.lockOwner
	env.lockMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var resp *filer_pb.DistributedLockResponse
	err := pb.WithFilerClient(filerAddr, env.GrpcDialOption, func(client filer_pb.FilerServiceClient) error {
		var callErr error
		resp, callErr = client.DistributedLock(ctx, &filer_pb.DistributedLockRequest{
			Name:       adminShellLockName,
			ExpireNs:   expire.Nanoseconds(),
			RenewToken: renewToken,
			Owner:      owner,
		})
		return callErr
	})
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	if resp.GetError() != "" {
		return fmt.Errorf("acquire lock: %s", resp.GetError())
	}

	env.lockMu.Lock()
	env.isLocked = true
	env.lockRenewToken = resp.GetRenewToken()
	env.lockMu.Unlock()
	_, _ = fmt.Fprintf(writer, "lock acquired (%s)\n", adminShellLockName)
	return nil
}

func (c *unlockCommand) Name() string { return "unlock" }

func (c *unlockCommand) Help() string {
	return "release admin shell distributed lock"
}

func (c *unlockCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	_ = args
	filerAddr := env.filerGRPCAddress()
	if filerAddr == "" {
		return fmt.Errorf("no filer address configured")
	}

	env.lockMu.Lock()
	renewToken := env.lockRenewToken
	env.lockMu.Unlock()
	if strings.TrimSpace(renewToken) == "" {
		return fmt.Errorf("lock not held by this shell session")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var resp *filer_pb.DistributedUnlockResponse
	err := pb.WithFilerClient(filerAddr, env.GrpcDialOption, func(client filer_pb.FilerServiceClient) error {
		var callErr error
		resp, callErr = client.DistributedUnlock(ctx, &filer_pb.DistributedUnlockRequest{
			Name:       adminShellLockName,
			RenewToken: renewToken,
		})
		return callErr
	})
	if err != nil {
		return fmt.Errorf("release lock: %w", err)
	}
	if resp.GetError() != "" {
		return fmt.Errorf("release lock: %s", resp.GetError())
	}

	env.lockMu.Lock()
	env.isLocked = false
	env.lockRenewToken = ""
	env.lockMu.Unlock()
	_, _ = fmt.Fprintf(writer, "lock released (%s)\n", adminShellLockName)
	return nil
}
