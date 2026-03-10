package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"GoBlob/goblob/command"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	go func() {
		for range sighup {
			command.HandleSIGHUP(os.Stderr)
		}
	}()

	exitCode := command.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(exitCode)
}
