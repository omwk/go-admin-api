// Package migrations embeds versioned SQL migrations into the CLI binary.
package migrations

import "embed"

// Files contains all migration pairs in version order.
//
//go:embed *.sql
var Files embed.FS
