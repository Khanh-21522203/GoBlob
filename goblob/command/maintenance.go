package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"GoBlob/goblob/server"
	"GoBlob/goblob/shell"
)

const defaultMaintenanceScript = "volume.balance -dryRun=true\nvolume.fix.replication -dryRun=true\nvolume.vacuum\ns3.clean.uploads -olderThan=24h"

func loadMaintenanceScript(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return defaultMaintenanceScript, nil
	}
	data, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("read maintenance script: %w", err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return defaultMaintenanceScript, nil
	}
	return content, nil
}

func startMaintenanceScheduler(
	ctx context.Context,
	ms *server.MasterServer,
	shellOpt *shell.ShellOption,
	script string,
	interval time.Duration,
	writer io.Writer,
) context.CancelFunc {
	maintCtx, cancel := context.WithCancel(ctx)
	if ms == nil || ms.Raft == nil || shellOpt == nil {
		return cancel
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if writer == nil {
		writer = io.Discard
	}

	run := func() {
		if !ms.Raft.IsLeader() {
			return
		}
		sh, err := shell.NewShell(shellOpt, nil, strings.NewReader(""), writer)
		if err != nil {
			_, _ = fmt.Fprintf(writer, "maintenance shell init failed: %v\n", err)
			return
		}
		if err := sh.RunScript(script); err != nil {
			_, _ = fmt.Fprintf(writer, "maintenance script failed: %v\n", err)
			return
		}
	}

	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-maintCtx.Done():
			return
		case <-timer.C:
			run()
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-maintCtx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()

	return cancel
}

func defaultMaintenanceShell(masterHTTPAddr, filerHTTPAddr string) *shell.ShellOption {
	return &shell.ShellOption{
		Masters:      []string{masterHTTPAddr},
		FilerAddress: filerHTTPAddr,
		Directory:    "/",
	}
}
