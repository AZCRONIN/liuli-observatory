// Package web 把打包后的前端（web/dist）嵌入二进制。
package web

import "embed"

// Dist 是前端打包产物。
//
//go:embed all:dist
var Dist embed.FS
