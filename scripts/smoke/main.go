// Command smoke supplies isolated infrastructure and observations for smoke.ps1.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var schemaPattern = regexp.MustCompile(`^s3_smoke_[a-f0-9]{32}$`)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "one smoke operation is required")
		os.Exit(1)
	}
	result, err := execute(ctx, os.Args[1])
	if err != nil {
		// Driver/HTTP errors can contain credentials or a secret-bearing URL.
		fmt.Fprintln(os.Stderr, "smoke helper: "+os.Args[1]+" failed; check dependency/configuration and scoped fixture")
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			fmt.Fprintln(os.Stderr, "SQLSTATE="+pg.Code+" table="+pg.TableName+" column="+pg.ColumnName+" constraint="+pg.ConstraintName)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(os.Stderr, "dependency operation deadline exceeded")
		}
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "cannot encode smoke observation")
		os.Exit(1)
	}
}

func execute(ctx context.Context, action string) (any, error) {
	switch action {
	case "create":
		return createSchema(ctx)
	case "drop":
		return dropSchema(ctx)
	case "migrate":
		return migrateSchema(ctx)
	case "sql":
		return queryJSON(ctx)
	case "fixtures":
		return installFixtures(ctx)
	case "credential":
		return checkCredential(ctx)
	case "redis-reserve":
		return reserveRedis(ctx)
	case "redis-clean":
		return cleanRedis(ctx)
	case "worker":
		return observeWorker()
	case "request":
		return requestHTTP(ctx)
	case "serve":
		return nil, serveTLS()
	default:
		return nil, errors.New("unknown smoke operation")
	}
}
