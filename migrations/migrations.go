// Package migrations embeds the versioned SQL migrations so the binary can
// apply them at startup without shipping the .sql files separately.
package migrations

import "embed"

// FS holds the goose-formatted migration files.
//
//go:embed *.sql
var FS embed.FS
