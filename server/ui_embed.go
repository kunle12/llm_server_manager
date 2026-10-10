//go:build embedui

package server

import "embed"

// uiFS holds the web UI build output. Populate it before building with this
// tag: `make ui` copies webui/dist into server/webui/dist.
//
//go:embed all:webui/dist
var uiFS embed.FS

const uiEmbedded = true
