//go:build !embedui

package server

import "embed"

// uiEmbedded reports whether the web UI assets are compiled into the binary.
// Default builds exclude them to keep the binary small and the build
// dependency-free; use -tags embedui to include them.
const uiEmbedded = false

// uiFS is empty in default builds; registerUIRoutes never touches it because
// uiEmbedded is false. Declared here so the code compiles in both modes.
var uiFS embed.FS
