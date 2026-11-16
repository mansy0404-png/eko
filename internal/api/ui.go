package api

import "embed"

// distFS holds the built Svelte UI. `npm run build` writes into this folder.
//
//go:embed all:dist
var distFS embed.FS
