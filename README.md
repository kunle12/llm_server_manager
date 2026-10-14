# LLM Fleet Manager

Manage `llama.cpp` (`llama-server`) model servers across one or more machines, from a REST API, a CLI, or a built-in web UI.

Each machine runs one `llm_server_manager` instance that can start and stop models defined in a JSON configuration file. A machine can additionally act as a **coordinator**: it lists and controls the models of other instances ("workers") and, optionally, exposes an **OpenAI-compatible proxy** (`/v1/...`) that routes requests to whichever machine runs the requested model.

## Motivation

[Llama.cpp](https://github.com/ggml-org/llama.cpp) is a popular open-source LLM inference engine. Running large models needs a lot of GPU VRAM or unified memory, so a single machine can usually run only one at a time. This service makes it easy to start and stop models on demand — locally or across a small fleet — via a REST API, a CLI, and a web UI.

> This is an experimental proof of concept, not production software. Largely "vibe coded" — use at your own risk.

## Features

- **Configuration-driven** — define models (and workers/proxy) in a JSON file
- **One model at a time per machine** — starting a new model while another runs is prevented
- **Auto-reload** — editing the config file (or saving via the API) reloads models/workers live; a running model is never restarted
- **Empty is fine** — the server starts with no models configured (with a warning), so you can add them later via reload or the UI
- **Coordinator / fleet** — one instance can list, start, and stop models on remote instances ("workers") running the same binary
- **OpenAI-compatible proxy** — optional `/v1/models`, `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`, routed to the machine running the model; toggle it live from the UI or config
- **Web UI** — view and control a fleet from the browser; edit configs with syntax highlighting and concurrent-edit protection
- **CLI** — `llm-cli` for scripting and remote control
- **Security** — optional API-key auth, CORS allow-list, per-IP rate limiting, request size caps
- **Daemon mode**, **graceful shutdown**, **optional llama-server logging**

## Requirements

- Go 1.21+
- `llama.cpp`'s `llama-server` available either next to the `llm_server_manager` binary or on `PATH` (override with `LLAMA_SERVER_PATH`)

## Build

```bash
git clone <this repo> && cd llm_server_manager

# Server without the web UI
make build                 # go build -o llm_server_manager .

# Server with the web UI embedded (served at /ui/ when started with -ui)
make build-ui              # make ui + go build -tags embedui

# CLI
make build-cli             # cd tools/cli && go build -o llm-cli
```

## Quick start

```bash
# Minimal config with one model
cat > llm_config.json <<'JSON'
{
  "models": [
    {
      "name": "llama-3.1-8b",
      "model_path": "/models/llama-3.1-8b.gguf",
      "context_size": 8192,
      "threads": 8,
      "port": 8081
    }
  ]
}
JSON

# Start (add -ui with an embedded build to serve the web UI)
./llm_server_manager -config=llm_config.json -listen=:8080 -ui
```

Then open <http://localhost:8080/ui/> (the manager serving the page is added automatically), or use the API:

```bash
curl http://localhost:8080/api/v1/models
curl -X POST http://localhost:8080/api/v1/models/llama-3.1-8b/start
```

## Command-line options

| Flag       | Default            | Description |
|------------|--------------------|-------------|
| `-config`  | `llm_config.json`  | Path to the configuration file (relative to the working directory) |
| `-listen`  | `:8080`            | Address to listen on. Binds all interfaces by default — use `127.0.0.1:8080` for loopback only |
| `-log`     | off                | Write each `llama-server`'s output to `/tmp/llama-server-{model}-{timestamp}.log` |
| `-daemon`  | off                | Run in the background; writes a PID file to `/tmp/llm_server_manager.pid` |
| `-retries` | `6`                | Max automatic restarts after a crash; `0` disables restarts |
| `-ui`      | off                | Serve the embedded web UI at `/ui/` (only in binaries built with `-tags embedui`) |

## Environment variables

| Variable              | Description |
|-----------------------|-------------|
| `LLM_MANAGER_API_KEY` | Enables API-key auth when set to exactly **16 alphanumeric** characters. Requests must then send it in the `api-key` header (the `/v1` proxy also accepts `Authorization: Bearer`). If set but malformed, auth is **disabled** with a warning |
| `LLAMA_SERVER_PATH`   | Path to the `llama-server` binary (defaults to `llama-server` next to the executable) |
| `LLM_ALLOWED_ORIGINS` | Comma-separated CORS allow-list (e.g. `http://localhost:3000`). Unset = all origins allowed |

## Configuration

Create `llm_config.json` from [`config.json.example`](config.json.example). Three top-level keys are supported:

```json
{
  "models": [
    { "name": "llama-7b", "alias": "gpt-4o-mini", "model_path": "/models/llama-7b.gguf", "context_size": 4096, "threads": 8, "port": 8081 },
    { "name": "custom", "launch_cmd": "llama-server -m /models/x.gguf -c 4096 --host 0.0.0.0 --port 8084" }
  ],
  "workers": [
    "http://gpu1.local:8080",
    { "url": "http://gpu2.local:8080", "name": "gpu2", "api_key": "abcdef1234567890" }
  ],
  "proxy": true
}
```

### Models

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | **Required**. Unique identifier; entries without a name are skipped (with a warning) |
| `alias` | string | Optional display/routing name, used by the proxy and shown in the UI |
| `model_path` | string | Path to the GGUF file |
| `launch_cmd` | string | Raw command to launch the model. When set, all other settings are ignored and the command runs via `bash -c` |
| `context_size` | int | Context window (omit to use llama.cpp's default) |
| `temperature` | float | Sampling temperature, 0.0–2.0 |
| `threads` | int | CPU threads |
| `port` | int | Listen port (default 8081) |
| `ngl` | int | GPU layers |
| `mmproj` | string | Path to an mmproj file (multimodal) |
| `mmap` | bool | When `false`, adds `--no-mmap` |
| `top_k`, `top_p` | int / float | Sampling thresholds |
| `presence_penalty`, `repetition_penalty` | float | Penalties (defaults 0.0 / 1.0) |
| `chat_template_kwargs` | string | Passed through to llama.cpp |
| `spec-draft-n-max` | int | Speculative decoding draft n max (adds `--spec-type draft-mtp`) |

Validation (on start and on `PUT /api/v1/config`): `name` required, `model_path` required (unless `launch_cmd`), `threads > 0`, `temperature` 0.0–2.0. A config with **no** named models is allowed — the server starts and logs a warning.

### Workers (coordinator mode)

A non-empty `workers` list enables coordinator mode. Each entry is either a plain URL or an object with `url`, optional `name` (defaults to the URL host), and optional `api_key` (sent as the worker's `api-key` header). Workers run this same binary, unchanged.

### Proxy

`"proxy": true` exposes the OpenAI-compatible `/v1/...` routes. The setting is applied **live**: toggling it (editing the file, or the web UI switch) takes effect on reload, no restart. When off, `/v1/...` responds `404` with an OpenAI-style error.

### Auto-reload

The config file's **directory** is watched (so editors that save by rename work), debounced 300 ms. On change the file is reloaded and models/workers/proxy are updated — a running `llama-server` is never restarted. Reload failures (invalid JSON) are logged and the previous configuration is kept.

## Web UI

Build with `make build-ui` and start with `-ui` to serve the SPA at `/ui/`.

- **Fleet view** — one card per machine (local + workers) with per-machine status, the running model, and inline start/stop. Workers are ordered as configured.
- **First run** — the page auto-adds the manager that served it as "This machine". Add more machines (URL + optional API key) in **⚙ Settings**; they're stored in the browser's `localStorage`.
- **Machine view** — the full model list for a machine, plus an **Edit config** action and a **Proxy** switch.
- **Config editor** — syntax-highlighted raw JSON. The server returns an `ETag`; the editor polls for external changes and refuses to overwrite them (see below).
- **Proxy switch** — per machine; flips `proxy` in that machine's config file (format-preserving) and applies immediately.

The UI is optional and independent: the REST API and CLI work without it.

### Concurrent edits

`GET /api/v1/config` returns an `ETag`. The editor polls with `If-None-Match`:

- a clean editor silently follows edits made on disk;
- a dirty editor shows a banner (*Reload from disk* / *Keep editing*);
- saving sends `If-Match`, so if the file changed underneath the server returns `409 Conflict` and the editor offers *Reload (discard mine)* / *Overwrite anyway* instead of clobbering the other change.

Other clients (e.g. the CLI) may PUT without `If-Match` for unconditional updates.

## API endpoints

All `/api/v1` endpoints return:

```json
{ "success": true, "message": "optional", "data": {} }
```

and, when `LLM_MANAGER_API_KEY` is set, require the `api-key` header.

| Method & path | Description |
|---------------|-------------|
| `GET /api/v1/models` | List configured models (sorted by name), with an `active` flag |
| `POST /api/v1/models/{model}/start` | Start a model |
| `DELETE /api/v1/models/{model}/stop` | Stop a model |
| `GET /api/v1/models/running` | Get the running model (or `success:false`, "no model is operating") |
| `GET /api/v1/config` | Raw config file text, plus an `ETag` header (supports `If-None-Match` → 304) |
| `PUT /api/v1/config` | Validate and atomically write a new config (raw JSON body); reloads immediately. Optional `If-Match` → `409` on a stale validator |
| `GET /api/v1/workers` | Worker list with health (empty when standalone) |
| `GET /api/v1/fleet` | Aggregated view: local machine first, then workers |
| `GET /api/v1/workers/{worker}/models` | A worker's model list |
| `POST /api/v1/workers/{worker}/models/{model}/start` | Start a model on a worker |
| `DELETE /api/v1/workers/{worker}/models/{model}/stop` | Stop a model on a worker |
| `GET /api/v1/proxy` | Runtime proxy state: `{"enabled": bool}` |
| `GET /v1/models` | OpenAI-format model list (proxy mode only) |
| `POST /v1/chat/completions`, `POST /v1/completions`, `POST /v1/embeddings` | Proxied to the machine running the model (streaming supported) |

### Examples

```bash
# List / start / stop
curl http://localhost:8080/api/v1/models
curl -X POST http://localhost:8080/api/v1/models/llama-7b/start
curl -X DELETE http://localhost:8080/api/v1/models/llama-7b/stop

# Read the config, then write it back (with conflict protection)
etag=$(curl -s -D - -o /dev/null http://localhost:8080/api/v1/config | awk -F': ' 'tolower($1)=="etag"{print $2}' | tr -d '\r')
curl -s http://localhost:8080/api/v1/config
curl -X PUT http://localhost:8080/api/v1/config -H "If-Match: $etag" --data-binary @llm_config.json

# Check the proxy state
curl http://localhost:8080/api/v1/proxy

# With authentication
curl -H "api-key: $LLM_MANAGER_API_KEY" http://localhost:8080/api/v1/models
```

## CLI tool

`tools/cli` builds `llm-cli` (see [tools/cli/README.md](tools/cli/README.md)):

```bash
cd tools/cli && go build -o llm-cli

./llm-cli list                       # list models
./llm-cli start llama-7b             # start a model
./llm-cli stop llama-7b              # stop a model
./llm-cli status                     # show the running model
./llm-cli fleet                      # coordinator fleet view (local + workers)

./llm-cli start llama-7b --worker gpu2   # start on a specific worker
./llm-cli -s http://192.168.1.100:8080 list

export LLM_MANAGER_API_KEY=abcdef1234567890   # auth (16 alphanumeric chars)
```

## Daemon mode

```bash
./llm_server_manager -daemon -config=llm_config.json -listen=:8080
kill $(cat /tmp/llm_server_manager.pid)
```

## Server status values

Reported in the running-server payload — `starting`, `running`, `stopping`. There is no `stopped` state: when nothing runs, `GET /api/v1/models/running` returns `success:false`.

## Security

- **Binding**: `-listen` defaults to `:8080`, i.e. **all interfaces**. On a shared network, run with `127.0.0.1:8080` and/or set `LLM_MANAGER_API_KEY`.
- **Authentication**: optional `api-key` header (16 alphanumeric chars, constant-time compared). If the key is malformed, auth is disabled with a warning.
- **CORS**: defaults to allowing all origins; restrict with `LLM_ALLOWED_ORIGINS`.
- **Rate limiting**: 10 requests/second per IP, burst 20.
- **Request sizes**: 1KB for most endpoints; **1MB** for `PUT /api/v1/config`; 10MB for `/v1/*` prompts.
- **PID file**: written with `0600` permissions.

## Development

```bash
go test ./...            # full suite
go test -race ./...      # concurrency-sensitive (config watcher, manager state)
make ui                  # build the web UI into webui/dist
make build-ui            # embed the UI and build the server
```

Layout:

```
main.go            entry point / flags
config/            JSON config loading (viper)
models/            data structures
manager/           llama.cpp process lifecycle
handlers/          REST handlers (models, config, workers, fleet, proxy status)
fleet/             coordinator: worker registry + client, fleet aggregation
proxy/             OpenAI-compatible /v1 proxy
server/            HTTP server, routing, middleware, config watcher, /ui/ embedding
webui/             Svelte 5 + Vite + TypeScript web UI
tools/cli/         Cobra-based CLI
docs/              design notes
```

## Troubleshooting

- **Server won't start** — check `llama-server` exists (next to the binary or via `LLAMA_SERVER_PATH`), the model file paths are readable, and the port is free.
- **Config changes not picked up** — confirm you edited the file passed to `-config`; the config must live on a local filesystem (fsnotify does not work on NFS/SMB/FUSE). A malformed config is rejected and the previous one stays active (check for `Failed to reload config`).
- **UI shows "No machines yet"** — the UI starts with an empty machine list and normally auto-adds the manager that served it. If it didn't (e.g. the UI is hosted separately), add the manager in **⚙ Settings**.
- **Worker doesn't appear in the UI** — the coordinator must list it under `workers`; check `GET /api/v1/fleet` and `GET /api/v1/workers` (offline workers show a reason such as *"Connection refused — the manager isn't running"*).
- **`/v1/...` returns 404** — the proxy is off. Enable `"proxy": true` (or use the UI switch); it applies live.
- **401 from the API/CLI** — `LLM_MANAGER_API_KEY` is set; send the same key in the `api-key` header (the CLI reads it from the environment).

## License

Demonstration project. Use at your own risk.
