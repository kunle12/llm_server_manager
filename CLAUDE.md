# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Quick Start

### Common Commands

```bash
# Install dependencies
go mod tidy

# Build the server
go build -o llm_server_manager

# Build the CLI tool
cd tools/cli && go build -o llm-cli && cd ..

# Run server with custom config and listen address
./llm_server_manager -config=config.json -listen=:8080

# Run in background (daemon mode)
nohup ./llm_server_manager -config=config.json -listen=:8080 > manager.log 2>&1 &
```

### Server Command Line Options

- `-config`: Path to configuration file (default: `llm_config.json`)
- `-listen`: Address to listen on (default: `:8080`)
- `-log`: Enable logging to `/tmp/llama-server-{model}-{timestamp}.log`
- `-daemon`: Run in background daemon mode

### Environment Variables

- `LLM_MANAGER_API_KEY`: API key for authentication (16 alphanumeric characters)
- `LLAMA_SERVER_PATH`: Path to llama-server binary (defaults to `llama-server`)
- `LLM_ALLOWED_ORIGINS`: Comma-separated list of allowed CORS origins (e.g., `http://localhost:3000,https://example.com`)

### CLI Tool Commands

```bash
# List all configured models
./llm-cli list

# Start a model
./llm-cli start llama-7b

# Stop a model
./llm-cli stop llama-7b

# Check running model
./llm-cli status

# Connect to remote server
./llm-cli -s http://192.168.1.100:8080 list

# Connect via HTTPS with API key authentication
export LLM_MANAGER_API_KEY="abcdef1234567890"
./llm-cli -s https://192.168.1.100:8080 list

# Skip TLS certificate verification (for self-signed certs)
./llm-cli -s https://192.168.1.100:8080 --skip-tls-verify list
```

See [tools/cli/README.md](tools/cli/README.md) for full CLI documentation.

### Prerequisites

- Go 1.21 or later
- llama.cpp installed and available in PATH (set `LLAMA_SERVER_PATH` to override, defaults to `llama-server`)

## High-Level Architecture

This is a **layered Go application** that manages multiple llama.cpp server instances through a REST API. The architecture follows a clear separation of concerns:

```
┌─────────────────────────────────────────┐
│           main.go (Entry Point)         │
│  • CLI flag parsing                     │
│  • Configuration validation             │
│  • Application lifecycle                │
└──────────────┬──────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────┐
│        server/server.go                 │
│  • HTTP server setup (gorilla/mux)     │
│  • Router configuration                 │
│  • Graceful shutdown handling           │
│  • Signal handling (SIGINT/SIGTERM)     │
└──────────────┬──────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────┐
│     handlers/handlers.go                │
│  • HTTP request handlers                │
│  • JSON response formatting             │
│  • CORS middleware                      │
│  • Request validation                   │
└──────────────┬──────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────┐
│       manager/manager.go                │
│  • Core business logic                  │
│  • Process management (start/stop)      │
│  • Thread-safe operations (sync.RWMutex)│
│  • llama.cpp command construction       │
└──────────────┬──────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────┐
│      config/config.go                   │
│  • Configuration loading (Viper)        │
│  • JSON parsing                         │
│  • Environment variable support         │
└──────────────┬──────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────┐
│       models/models.go                  │
│  • Data structures                      │
│  • API response types                   │
│  • Server status constants              │
└─────────────────────────────────────────┘
```

### Key Design Patterns

1. **Dependency Injection**: Logger and manager are injected into handlers
2. **Thread Safety**: `sync.RWMutex` protects concurrent access to server state
3. **Graceful Shutdown**: Server stops running processes on termination signals
4. **Configuration-Driven**: Models defined in JSON config file

### Thread Safety Model

The application uses `sync.RWMutex` for thread-safe operations:
- **Multiple concurrent reads**: `ListModels()`, `ReloadConfigs()` use RLock
- **Exclusive writes**: `StartModel()`, `StopCurrent()`, `GetCurrentServer()` use Lock
- **Race condition protection**: Server state updates are atomic

### Cancellation Model

Server lifecycle uses `context.Context` for cancellation:
- `StartModel()` creates a new context and cancel function
- `StopCurrent()` calls `cancelFunc()` to signal the server goroutine
- Server goroutine monitors `ctx.Done()` to handle stop requests

### Process Management

The `manager` package handles llama.cpp process lifecycle:
- **Start**: Launches `llama-server` as subprocess
- **Monitor**: Tracks PID, status, and start time
- **Stop**: Sends SIGKILL to running process
- **Validation**: Checks model file existence and llama.cpp availability

## Core Components

### 1. ServerManager (manager/manager.go)

**Responsibilities**:
- Start/stop llama.cpp servers
- Validate model configurations
- Track running server state
- Build command-line arguments for llama.cpp

**Key methods**:
- `StartModel(modelName)`: Starts a model server (prevents duplicate servers)
- `StopCurrent()`: Stops the currently running server
- `ListModels()`: Returns all configured models
- `GetCurrentServer()`: Returns running server info

**Thread Safety**: Uses `sync.RWMutex` for all state access

### 2. HTTP Handlers (handlers/handlers.go)

**Endpoints**:
- `GET /api/v1/models`: List all configured models (includes `active` field)
- `POST /api/v1/models/{model}/start`: Start a model server
- `DELETE /api/v1/models/{model}/stop`: Stop a model server
- `GET /api/v1/models/running`: Get currently running model

**Features**:
- CORS support for cross-origin requests
- Consistent JSON response format
- Input validation
- Error handling with appropriate HTTP status codes

### 3. Configuration (config/config.go + models/models.go)

**Configuration Structure** (`config.json`):
```json
{
  "models": [
    {
      "name": "llama-7b",
      "model_path": "/path/to/model.gguf",
      "context_size": 4096,
      "temperature": 0.7,
      "threads": 8,
      "port": 8081,
      "ngl": 32,
      "mmap": false,
      "spec-draft-n-max": 10
    },
    {
      "name": "custom-model",
      "launch_cmd": "llama-server -m /path/to/model.gguf -c 4096 --host 0.0.0.0 --port 8082"
    }
  ]
}
```

**ModelConfig Fields**:
- `name`: Unique identifier
- `model_path`: Path to GGUF model file
- `context_size`: Maximum context window (optional, omit to use llama.cpp default)
- `temperature`: Sampling temperature (0.0-2.0)
- `threads`: CPU threads to use
- `port`: Server listen port (optional, defaults to 8081)
- `ngl`: Number of GPU layers (optional, positive integer)
- `mmap`: Disable memory mapping (optional, adds `--no-mmap` flag when set to false)
- `spec-draft-n-max`: Speculative decoding draft n max (optional, adds `--spec-type draft-mtp --spec-draft-n-max` when set)
- `launch_cmd`: Raw CLI command to launch the model (optional, when set all other settings are ignored, command is executed via `bash -c`)

### 4. Application Lifecycle (server/server.go)

**Startup Sequence**:
1. Load and validate configuration
2. Create ServerManager with logger
3. Setup HTTP router and handlers
4. Start HTTP server
5. Wait for shutdown signal

**Shutdown Sequence**:
1. Receive SIGINT/SIGTERM
2. Shutdown HTTP server (30s timeout)
3. Stop running llama.cpp process
4. Exit cleanly

## API Reference

### Response Format

All endpoints return JSON in this format:
```json
{
  "success": true|false,
  "message": "optional message",
  "data": {}
}
```

### Server Status Values

- `stopped`: No server running
- `starting`: Server initializing
- `running`: Server active
- `stopping`: Server shutting down

## Important Implementation Details

### HTTP Middleware

The server applies middleware in this order (server/server.go:174-182):

1. **Rate Limiting**: 10 requests/second per IP with 20 burst capacity
   - Uses token bucket algorithm from `golang.org/x/time/rate`
   - Rate limited before authentication (applies to all requests)
   - Excess requests return `429 Too Many Requests`

2. **Request Size Limit**: 1KB maximum body size
   - Prevents memory exhaustion from large request bodies
   - Returns `413 Request Entity Too Large` for oversized bodies

3. **CORS**: Adds appropriate headers based on origin
   - If `LLM_ALLOWED_ORIGINS` set: only allowed origins get CORS headers
   - Otherwise: all origins permitted (legacy behavior)

4. **API Key Authentication**: Validates `api-key` header for `/api/v1/*` routes
   - Only applied to API routes, not health check endpoints

### llama.cpp Command Construction

The manager builds this command structure (manager/manager.go:152-182):
```bash
llama-server -m <model_path> -c <context_size> --temp <temperature> -t <threads> --no-webui --host 0.0.0.0 --port <port>
```
With optional flags: `--log-disable` (when logging disabled), `--mmproj <path>` (if configured), `-ngl <layers>` (if configured), `--no-mmap` (if `mmap` is set to false), `--spec-type draft-mtp --spec-draft-n-max <n>` (if `spec-draft-n-max` is set and positive).

Note: `-c <context_size>` is only added when `context_size` is configured and positive.

Override the binary path with `LLAMA_SERVER_PATH` environment variable.

### Error Handling

The application handles:
- Configuration errors (missing file, invalid JSON, empty models)
- Duplicate server prevention
- Model not found errors
- Wrong model stop attempts
- Process execution failures
- Graceful shutdown on signals

### Logging

All operations log to stdout with timestamp:
```
[LLM Manager] Server starting on :8080
[LLM Manager] Starting llama.cpp server for model: llama-7b
[LLM Manager] Server started successfully with PID: 12345
[LLM Manager] Server stopped for model: llama-7b
```

## Dependencies

### Server Dependencies
- **github.com/gorilla/mux v1.8.1**: HTTP router
- **github.com/spf13/viper v1.18.2**: Configuration management
- **github.com/fsnotify/fsnotify v1.7.0**: File watching for auto-reload
- **golang.org/x/time v0.5.0**: Rate limiting

### CLI Dependencies
- **github.com/spf13/cobra v1.8.0**: CLI framework
- **github.com/spf13/viper v1.18.2**: Configuration management

## Security Considerations

### Authentication

- API key authentication via `LLM_MANAGER_API_KEY` environment variable
- Key must be exactly 16 alphanumeric characters
- Key validated on each API request via `api-key` header
- Uses `crypto/subtle.ConstantTimeCompare` to prevent timing attacks
- API key validated at CLI startup with warning if invalid format

### CORS Security

- Default: CORS enabled for all origins (`Access-Control-Allow-Origin: *`)
- Restricted mode: Set `LLM_ALLOWED_ORIGINS` to comma-separated origins
- Requests from non-allowed origins receive `403 Forbidden`
- Vary: Origin header always set for cache-aware proxies

### Rate Limiting

- Per-IP rate limiting: 10 requests/second, burst of 20
- Excess requests receive `429 Too Many Requests`
- Uses `golang.org/x/time/rate` for token bucket algorithm
- Rate limiter applied before authentication
- Periodic cleanup of stale entries every 5 minutes (prevents memory leak)

### Request Size Limits

- Request body limited to 1KB
- Prevents memory exhaustion attacks
- Uses `http.MaxBytesReader` handler

### PID File Permissions

- Daemon PID file written with `0600` permissions (readable only by owner)
- Location: `/tmp/llm_server_manager.pid`

### TLS Verification

- CLI supports `--skip-tls-verify` flag for self-signed certificates
- Uses reusable `TLSClient` with custom transport for TLS skip
- Default: TLS certificates fully verified

## Troubleshooting

**Server won't start**:
- Check llama.cpp: `llama-cli --help`
- Verify model file exists and is readable
- Ensure port is not in use
- Review logs for errors

**Permission denied**:
- Ensure llama-cli is executable
- Check model file permissions

**Model won't load**:
- Verify model path is correct
- Ensure model is in GGUF format
- Check available system memory

## Development Notes

- The application is designed to run one llama.cpp server at a time
- ServerManager prevents starting a new server if one is already running
- The `host` field does not exist in config (hardcoded to 0.0.0.0 in manager.go:164)
- No tests exist (test files would be `*_test.go`)
- Model config validation: Name required, ModelPath required, Threads > 0, Temperature 0.0-2.0
- Cancellation uses `context.Context` (not chan chan struct{}) to avoid deadlock
- Log file handle properly closed after `cmd.Wait()` before retry/exit
- Viper state reset on config load for consistency with reload
- Rate limiter has periodic cleanup to prevent memory leak
- Watcher properly closed on error paths and during shutdown

### CLI Tool Structure (tools/cli/)

```
tools/cli/
├── main.go              # Entry point
├── commands/
│   ├── root.go         # Root command, HTTP client, TLS skip verify
│   ├── list.go         # list command
│   ├── start.go        # start command
│   ├── stop.go         # stop command
│   ├── status.go       # status command
│   └── version.go      # version command
└── llmcontrol/
    └── models.go       # API response types
```

**CLI Flags**:
- `-s, --server`: Server URL (default: `http://localhost:8080`)
- `--skip-tls-verify`: Skip TLS certificate verification (for self-signed certs)
- `--api-key`: API key for authentication (alternative to env var)
- `--timeout`: Request timeout in seconds (default: 30)
