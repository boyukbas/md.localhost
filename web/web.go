// Package web holds the browser-side assets compiled into the binary.
package web

import "embed"

// FS contains the page templates and the static assets served under /assets/.
// The two large vendored libraries are stored pre-compressed and are served
// with Content-Encoding: gzip.
//
//go:embed page.html index.html assets
var FS embed.FS
