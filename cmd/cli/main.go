package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/omwk/go-admin-api/internal/bootstrap"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) (err error) {
	if len(args) == 2 && args[0] == "permissions" && args[1] == "export" {
		return permission.Export(out)
	}
	if len(args) == 8 && args[0] == "permissions" && args[1] == "grant" && args[2] == "--role" && args[4] == "--codes" && args[6] == "--release" {
		return grant(ctx, out, args[3], strings.Split(args[5], ","), args[7])
	}
	if len(args) == 1 && args[0] == "seed" {
		return seed(ctx, out)
	}
	if len(args) != 2 || args[0] != "migrate" || !validMigrationCommand(args[1]) {
		return errors.New("usage: cli migrate up|down|version | seed | permissions export | permissions grant --role <code> --codes <csv> --release <id>")
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	version, err := database.Migrate(ctx, args[1])
	if err != nil {
		return errors.New("migration failed; check database availability, schema state and lock contention")
	}
	_, err = fmt.Fprintf(out, "version=%d dirty=%t\n", version.Version, version.Dirty)
	return err
}

func grant(ctx context.Context, out io.Writer, role string, codes []string, release string) (err error) {
	cfg, err := config.Load(os.Getenv("CH_CONFIG_PATH"))
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	database, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	shared, err := cache.Open(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, shared.Close()) }()
	service, err := identity.NewPermissionReleaseService(bootstrap.NewPermissionReleaseStore(database), func(ctx context.Context, event audit.Event) error {
		return audit.Record(ctx, database, event)
	})
	if err != nil {
		return err
	}
	ctx = logger.WithTraceID(ctx, "permissions-release:"+release)
	result, err := service.Apply(ctx, 1, identity.PermissionReleaseRequest{RoleCode: role, Codes: codes, Release: release})
	if err != nil {
		return err
	}
	if result.Changed {
		versions := authz.New(shared)
		versions.Pending(ctx, 1, result.AffectedUser)
	}
	_, err = fmt.Fprintf(out, "release=%s summary=%s added=%d changed=%t users=%d\n", result.Release, result.Summary, result.Added, result.Changed, len(result.AffectedUser))
	return err
}

func openDatabase(ctx context.Context) (*db.DB, error) {
	cfg, err := config.Load(os.Getenv("CH_CONFIG_PATH"))
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return db.Open(ctx, cfg.DB)
}

func seed(ctx context.Context, out io.Writer) (err error) {
	password := os.Getenv("CH_SEED_ADMIN_PASSWORD")
	if password == "" {
		return errors.New("CH_SEED_ADMIN_PASSWORD is required")
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	if err := bootstrap.SeedIdentity(ctx, database, password); err != nil {
		return &errs.AppError{Code: errs.ErrInternal, HTTPStatus: 500, Message: "seed failed; check database schema and initialization settings", Cause: err}
	}
	_, err = fmt.Fprintln(out, "seed completed")
	return err
}

func validMigrationCommand(command string) bool {
	return command == "up" || command == "down" || command == "version"
}
