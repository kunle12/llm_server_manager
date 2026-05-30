# LlamaCPP Model Service Manager

This service manages operation of a llama.cpp server, `llama-server`, to provide opensource Large Language Model (LLM)s via a REST API. It allows starting and stopping of multiple LLM models defined in a configuration file, running each model in its own `llama-server` process instance.

## Movivation

[Llama.cpp](https://github.com/ggml-org/llama.cpp) is a popular open-source LLM inference engine for running LLMs locally. As most of current LLMs have parameters in the billions, they require significant system resources, specially in terms of GPU VRAM or Unified Memory (RAM + Swap). Running multiple models simultaneously on a single machine can be challenging. A machine like Mac Studio with 127G Unified memory can barely run a 3bit quantised Qwen 235B-A22B Instruct model. Running multiple models will require swapping models on the system[1]. This service allows users to easily start and stop different LLM models on demand via a REST API, managing the underlying `llama-server` processes safely.

(The real purpose)
This service is built as an experimental PoC to prove to myself that opensource LLM, in this case MiniMax M2.1, plus code agent such as Claud Code can be used to build useful applications in a cost effective manner without relying on expensive API services from OpenAI, Anthropic, etc. This project is not intended for production use, 99.9% of the code is *vibe coded*, use at your own risk.

[1] Llama.cpp will support dynamic model loading in future releases.

## Features

- **Configuration-driven**: Define multiple LLM models in a JSON configuration file
- **REST API**: Control server operations via HTTP endpoints
- **Process Management**: Safe startup and shutdown of llama.cpp server processes
- **Daemon Mode**: Runs as a background service
- **CORS Support**: Enable cross-origin requests
- **Graceful Shutdown**: Properly stops servers on application termination
- **Thread-Safe**: Concurrent-safe server management with mutex locks
- **Logging**: Optional logging of llama-server output to files
- **Auto-Reload**: Configurations are reloaded automatically when the config file is modified

## Architecture

```
llm_server_manager/
├── main.go              # Application entry point
├── config/
│   └── config.go       # Configuration loading
├── models/
│   └── models.go       # Data structures
├── manager/
│   └── manager.go      # Server management logic
├── handlers/
│   └── handlers.go     # HTTP request handlers
├── server/
│   └── server.go       # HTTP server setup
└── tools/
    └── cli/            # CLI tool for remote management
```

## Prerequisites

- Go 1.21 or later
- llama.cpp installed and available in PATH (set `LLAMA_SERVER_PATH` env var to override)

## Installation

```bash
cd llm_server_manager
go mod tidy
go build -o llm_server_manager
```

## Configuration

Create a `config.json` file based on `config.json.example`:

```json
{
  "models": [
    {
      "name": "llama-7b",
      "model_path": "/path/to/llama-7b.gguf",
      "context_size": 4096,
      "temperature": 0.7,
      "threads": 8,
      "port": 8081,
      "ngl": 32,
      "spec-draft-n-max": 10
    }
  ]
}
```

### Configuration Fields

| Field            | Type     | Description                                      |
|-----------------|----------|--------------------------------------------------|
| name             | string   | Unique identifier for the model                  |
| model_path       | string   | Path to the GGUF model file                      |
| context_size     | int      | Maximum context window size (optional)           |
| temperature      | float64  | Sampling temperature (0.0-2.0)                   |
| threads          | int      | Number of CPU threads to use                     |
| port             | int      | Port for llama.cpp server to listen              |
| ngl              | int      | Number of GPU layers (optional)                  |
| mmproj           | string   | Path to mmproj file (optional)                   |
| top_k            | int      | Top-K sampling threshold (optional)              |
| top_p            | float64  | Top-P sampling threshold 0.0-1.0 (optional)      |
| mmap             | bool     | Disable memory mapping (optional, adds --no-mmap)|
| spec-draft-n-max | int      | Speculative decoding draft n max (optional)      |
| chat_template_kwargs | string | Chat template kwargs (optional)              |
| launch_cmd       | string   | Raw CLI command to launch (optional, ignores other settings) |

### Auto-Reload Configurations

The server automatically watches the configuration file for changes. When the file is modified:

1. The server detects the change
2. Reloads the configuration from the file
3. Updates the available models without restarting

Example log output when config is reloaded:
```
[LLM Manager] Config file changed, reloading...
[LLM Manager] Configuration reloaded successfully
```

New models added to the configuration will be immediately available via the API:
```bash
curl http://localhost:8080/api/v1/models
```

**Note**: Only model configurations are reloaded. Running servers are not affected.

## Usage

### Start the Manager

```bash
./llm_server_manager -config=config.json -listen=:8080
```

### Start with Logging

```bash
./llm_server_manager -config=config.json -listen=:8080 -log
```

Logs will be saved to `/tmp/llama-server-{model_name}-{timestamp}.log`

### Command Line Options

- `-config`: Path to configuration file (default: config.json)
- `-listen`: Address to listen on (default: :8080)
- `-log`: Enable logging llama-server output to `/tmp/llama-server-{model}-{timestamp}.log` (default: disabled)
- `-daemon`: Run in background daemon mode (default: disabled)

### Run in Background (Daemon)

```bash
./llm_server_manager -daemon -config=config.json -listen=:8080
```

The daemon flag will:
- Spawn the process in a new session
- Detach from the terminal
- Continue running in the background
- Return immediately to the shell
- Create a PID file at `/tmp/llm_server_manager.pid`

You can stop the daemon using the PID file:
```bash
kill $(cat /tmp/llm_server_manager.pid)
```

Alternatively, you can use nohup for manual daemonization:

```bash
nohup ./llm_server_manager -config=config.json -listen=:8080 > manager.log 2>&1 &
```

## API Endpoints

### 1. List Models

**Endpoint**: `GET /api/v1/models`

Returns a list of all configured models with their configuration details.

**Response**:
```json
{
  "success": true,
  "data": {
    "models": [
      {
        "name": "llama-7b",
        "model_path": "/path/to/model.gguf",
        "context_size": 4096,
        "temperature": 0.7,
        "threads": 8,
        "port": 8081,
        "active": true
      }
    ]
  }
}
```

### 2. Start Model Server

**Endpoint**: `POST /api/v1/models/{model}/start`

Starts a llama.cpp server with the specified model.

**Parameters**:
- `model`: Name of the model to start (must match configuration)

**Success Response**:
```json
{
  "success": true,
  "message": "server starting",
  "data": {
    "server": {
      "model_config": { ... },
      "pid": 12345,
      "status": "running",
      "start_time": "2025-12-20T10:30:00Z"
    }
  }
}
```

**Error Response** (when server already running):
```json
{
  "success": false,
  "message": "a server is already running with model: llama-7b"
}
```

**Error Response** (when model not found):
```json
{
  "success": false,
  "message": "model 'invalid-model' not found in configuration"
}
```

### 3. Stop Model Server

**Endpoint**: `DELETE /api/v1/models/{model}/stop`

Stops the running server for the specified model.

**Parameters**:
- `model`: Name of the model to stop

**Success Response**:
```json
{
  "success": true,
  "message": "server stopped successfully"
}
```

**Error Response** (when no server running):
```json
{
  "success": false,
  "message": "no server is currently running"
}
```

**Error Response** (when wrong model):
```json
{
  "success": false,
  "message": "server is running model 'llama-7b', not 'codellama'"
}
```

### 4. Get Running Model

**Endpoint**: `GET /api/v1/models/running`

Returns information about the currently running model server.

**Success Response** (when server is running):
```json
{
  "success": true,
  "message": "model 'llama-7b' is currently operating"
}
```

**Success Response** (when no server is running):
```json
{
  "success": false,
  "message": "no model is operating"
}
```

## Example Usage with curl

### List all models
```bash
curl http://localhost:8080/api/v1/models
```

### Start a model
```bash
curl -X POST http://localhost:8080/api/v1/models/llama-7b/start
```

### Stop a model
```bash
curl -X DELETE http://localhost:8080/api/v1/models/llama-7b/stop
```

### Check running model
```bash
curl http://localhost:8080/api/v1/models/running
```

## CLI Tool

A command-line tool is available in `tools/cli/` for remote management:

```bash
# Build the CLI
cd tools/cli
go build -o llmcontrol .

# List models
./llmcontrol list

# Start a model
./llmcontrol start llama-7b

# Stop a model
./llmcontrol stop llama-7b

# Check running model
./llmcontrol status

# Connect to remote server
./llmcontrol -s http://192.168.1.100:8080 list
```

See [tools/cli/README.md](tools/cli/README.md) for full documentation.

## Server Status Values

- `stopped`: No server is running
- `starting`: Server is being initialized
- `running`: Server is active and serving requests
- `stopping`: Server is being shut down

## Error Handling

The application handles the following scenarios:

1. **Configuration Errors**: Missing or invalid config file
2. **Duplicate Server**: Prevents starting a server when one is already running
3. **Model Not Found**: Returns error when attempting to start unknown model
4. **Wrong Model**: Prevents stopping a server running a different model
5. **Process Errors**: Handles failures in starting/killing llama.cpp processes
6. **Graceful Shutdown**: Stops servers properly on SIGINT/SIGTERM

## Logging

All operations are logged to stdout with timestamps:
```
[LLM Manager] Server starting on :8080
[LLM Manager] Starting llama.cpp server for model: llama-7b
[LLM Manager] Server started successfully with PID: 12345
[LLM Manager] Server stopped for model: llama-7b
[LLM Manager] Shutting down server...
```

## Thread Safety

The application uses `sync.RWMutex` to ensure thread-safe operations:
- Multiple concurrent reads (ListModels, GetCurrentServer)
- Exclusive write access during server start/stop operations
- Protection against race conditions in server state management

## Security Considerations

- CORS is enabled for all endpoints
- No authentication is implemented (add reverse proxy if needed)
- Process execution uses direct command execution
- Validate model names to prevent command injection

## Development

### Project Structure

```
llm_server_manager/
├── main.go              # Entry point
├── go.mod               # Module definition
├── go.sum               # Dependency checksums
├── config/
│   └── config.go       # Viper-based config loading
├── models/
│   └── models.go       # Data models
├── manager/
│   └── manager.go      # Server management
├── handlers/
│   └── handlers.go     # HTTP handlers
├── server/
│   └── server.go       # HTTP server
└── tools/
    └── cli/            # CLI tool (cobra-based)
```

### Dependencies

- **github.com/gorilla/mux**: HTTP router
- **github.com/spf13/viper**: Configuration management
- **github.com/fsnotify/fsnotify**: File system notifications for auto-reload

## License

This is a demonstration project. Use at your own risk.

## Troubleshooting

### Server won't start
- Check llama.cpp is installed: `llama-server --help`
- Verify model file exists and is readable
- Check the port is not already in use
- Review logs for errors

### Permission denied
- Ensure llama-server is executable
- Check file permissions on model files

### Model won't load
- Verify model path is correct
- Ensure model file is in GGUF format
- Check available system memory
