package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/omwk/go-admin-api/internal/bootstrap"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
)

func auditPrune(ctx context.Context, args []string, out io.Writer) (err error) {
	flags := flag.NewFlagSet("audit-prune", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	before := flags.String("before", "", "exclusive RFC3339 cutoff")
	force := flags.Bool("force", false, "allow production pruning")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *before == "" {
		return errors.New("usage: cli audit-prune --before <RFC3339> [--force]")
	}
	cutoff, err := time.Parse(time.RFC3339Nano, *before)
	if err != nil {
		return errors.New("audit-prune --before must be RFC3339")
	}
	cfg, err := config.Load(os.Getenv("CH_CONFIG_PATH"))
	if err != nil {
		return err
	}
	if cfg.App.Env == "prod" && !*force {
		return errors.New("audit-prune in prod requires --force")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	database, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	count, err := bootstrap.PruneAudit(ctx, database, cutoff)
	if err != nil {
		return errors.New("audit prune failed; no cleanup committed")
	}
	_, err = fmt.Fprintf(out, "deleted=%d\n", count)
	return err
}
