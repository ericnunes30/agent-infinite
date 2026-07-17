package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/agent-infinite/agent-infinite/backend/internal/app"
	"github.com/agent-infinite/agent-infinite/backend/internal/hookbridge"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "hook-forward" {
		if err := hookbridge.Forward(ctx, os.Stdin, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		return
	}
	if err := app.Run(ctx, os.Stdin, os.Stdout, os.Stderr); err != nil {
		log.New(os.Stderr, "agent-infinite: ", log.LstdFlags).Fatal(err)
	}
}
