//go:build !webassets

package webassets

import "embed"

// The fallback keeps ordinary go build/test commands working from a clean
// checkout. Production builds use assets_built.go after Vite creates dist.
//
//go:embed all:placeholder
var dist embed.FS

const distRoot = "placeholder"
