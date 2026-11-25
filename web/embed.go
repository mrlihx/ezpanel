// Package web embeds the compiled panel frontend.
package web

import "embed"

//go:embed dist
var Assets embed.FS
