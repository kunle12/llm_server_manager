.PHONY: ui build build-ui build-cli dist test clean

# Build the web UI and copy it into the embed directory used by
# `go build -tags embedui`. The UI must be built before an embedded build.
ui:
	cd webui && npm ci && npm run build
	find server/webui/dist -mindepth 1 -not -name .keep -delete
	cp -R webui/dist/. server/webui/dist/

# Default server build: no web UI embedded (-ui is accepted but inert).
build:
	go build -o llm_server_manager .

# Server build with the web UI embedded; serve it with -ui.
build-ui: ui
	go build -tags embedui -o llm_server_manager .

build-cli:
	cd tools/cli && go build -o llm-cli && cd ../..

# Release binaries (dist/ is gitignored). Builds the web UI first so the
# embedded binary contains the current frontend. Requires Node.js/npm.
DIST_LDFLAGS := -s -w
dist: ui
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o dist/llm_server_manager-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o dist/llm_server_manager-darwin-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o dist/llm_server_manager-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o dist/llm_server_manager-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -tags embedui -o dist/llm_server_manager-ui-darwin-arm64 .
	cd tools/cli && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o ../../dist/llm-cli-darwin-amd64 .
	cd tools/cli && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o ../../dist/llm-cli-darwin-arm64 .
	cd tools/cli && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o ../../dist/llm-cli-linux-amd64 .
	cd tools/cli && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(DIST_LDFLAGS)' -o ../../dist/llm-cli-linux-arm64 .

test:
	go test ./...

clean:
	rm -f llm_server_manager tools/cli/llm-cli
