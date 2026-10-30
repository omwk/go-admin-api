package main

import (
	"fmt"
	"os"

	"github.com/omwk/go-admin-api/internal/kernel/config"
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
	return cfg.Validate()
}
