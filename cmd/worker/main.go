package main

import (
	"context"
	"fmt"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/worker"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv("CH_CONFIG_PATH"))
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	server, err := worker.NewServer(func(context.Context, worker.TaskPayload) error { return nil })
	if err != nil {
		return err
	}
	return server.Run(context.Background())
}
