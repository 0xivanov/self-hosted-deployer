package migrations

import "embed"

// FS contains the SQLite schema migrations used by the repository package.
//
//go:embed *.sql
var FS embed.FS
