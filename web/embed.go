// Package web embeds the compiled frontend. It lives here because go:embed
// can't reach parent directories.
package web

import "embed"

// Dist holds the Vite build output.
//
//go:embed all:dist
var Dist embed.FS
