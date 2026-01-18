# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Quick Start

### Common Commands

```bash
# Install dependencies
go mod tidy

# Build the application
go build -o llm_server_manager

# Run with custom config and listen address
./llm_server_manager -config=config.json -listen=:8080

# Run in background (daemon mode)
nohup ./llm_server_manager -config=config.json -listen=:8080 > manager.log 2>&1 &
```

### Command Line Options

- `-config`: Path to configuration file (default: `llm_config.json`)
- `-listen`: Address to listen on (default: `:8080`)

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
- **Multiple concurrent reads**: `ListModels()`, `GetCurrentServer()` use RLock
- **Exclusive writes**: `StartModel()`, `StopCurrent()` use Lock
- **Race condition protection**: Server state updates are atomic

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
- `GET /api/v1/models`: List all configured models
- `POST /api/v1/models/{model}/start`: Start a model server
- `DELETE /api/v1/models/{model}/stop`: Stop a model server

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
      "port": 8081
    }
  ]
}
```

**ModelConfig Fields**:
- `name`: Unique identifier
- `model_path`: Path to GGUF model file
- `context_size`: Maximum context window
- `temperature`: Sampling temperature (0.0-2.0)
- `threads`: CPU threads to use
- `port`: Server listen port (optional, defaults to 8081)

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

### llama.cpp Command Construction

The manager builds this command structure (manager/manager.go:152-182):
```bash
llama-server -m <model_path> -c <context_size> --temp <temperature> -t <threads> --no-webui --host 0.0.0.0 --port <port>
```
With optional flags: `--log-disable` (when logging disabled), `--mmproj <path>` (if configured).

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

- **github.com/gorilla/mux v1.8.1**: HTTP router
- **github.com/spf13/viper v1.18.2**: Configuration management

## Security Considerations

- CORS enabled for all endpoints
- No authentication (add reverse proxy if needed)
- Process execution uses direct command execution
- Model names validated to prevent command injection
- No input sanitization for model paths (trusts config file)

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
- No .gitignore rules for binary or log files (consider adding)
