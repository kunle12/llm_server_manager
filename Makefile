.PHONY: ui build build-ui build-cli test clean

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

test:
	go test ./...

clean:
	rm -f llm_server_manager tools/cli/llm-cli
