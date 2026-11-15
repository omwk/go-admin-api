package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omwk/go-admin-api/internal/bootstrap"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

func dictionaries(ctx context.Context, out io.Writer, reset bool) (err error) {
	cfg, err := config.Load(os.Getenv("CH_CONFIG_PATH"))
	if err != nil {
		return err
	}
	if reset && strings.EqualFold(strings.TrimSpace(cfg.App.Env), "prod") {
		return errors.New("dict-reset is forbidden in prod")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	database, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	maintain := bootstrap.BackfillDictionaries
	command := "dict-backfill"
	if reset {
		maintain, command = bootstrap.ResetDictionaries, "dict-reset"
	}
	results, maintenanceErr := maintain(ctx, database)
	for _, result := range results {
		if _, err := fmt.Fprintf(out, "tenant=%d dicts=%d->%d items=%d->%d\n", result.TenantID, result.BeforeDicts, result.AfterDicts, result.BeforeItems, result.AfterItems); err != nil {
			return errors.Join(maintenanceErr, err)
		}
	}
	if maintenanceErr != nil {
		return &errs.AppError{Code: errs.ErrInternal, HTTPStatus: 500, Message: command + " failed; completed tenants are listed above; check database availability and schema", Cause: maintenanceErr}
	}
	_, err = fmt.Fprintf(out, "%s completed tenants=%d\n", command, len(results))
	return err
}
