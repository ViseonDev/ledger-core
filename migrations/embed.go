// Package migrations embeds this project's SQL migrations so the service can
// apply them without shipping loose files alongside the binary.
package migrations

import "embed"

// FS holds every migration file, named <version>_<title>.<up|down>.sql.
//
//go:embed *.sql
var FS embed.FS
