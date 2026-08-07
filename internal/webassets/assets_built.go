//go:build webassets

package webassets

import "embed"

//go:embed all:dist
var dist embed.FS

const distRoot = "dist"
