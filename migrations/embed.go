// Package migrations embeds the ordered SQLite schema migrations.
package migrations

import "embed"

// Files contains all numbered SQL migration files.
//
//go:embed *.sql
var Files embed.FS
