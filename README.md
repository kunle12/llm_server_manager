# LLM Server Manager

A professional Go application for managing multiple llama.cpp server instances with a REST API.

## Features

- **Configuration-driven**: Define multiple LLM models in a JSON configuration file
- **REST API**: Control server operations via HTTP endpoints
- **Process Management**: Safe startup and shutdown of llama.cpp server processes
- **Daemon Mode**: Runs as a background service
- **CORS Support**: Enable cross-origin requests
- **Graceful Shutdown**: Properly stops servers on application termination
- **Thread-Safe**: Concurrent-safe server management with mutex locks
- **Logging**: Optional logging of llama-server output to files

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
└── server/
    └── server.go       # HTTP server setup
```

## Prerequisites

- Go 1.21 or later
- llama.cpp installed and available in PATH
- llama-cli binary (or modify manager.go to use the correct command)

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
      "host": "127.0.0.1"
    }
  ]
}
```

### Configuration Fields

| Field        | Type    | Description                          |
|-------------|---------|--------------------------------------|
| name        | string  | Unique identifier for the model      |
| model_path  | string  | Path to the GGUF model file          |
| context_size| int     | Maximum context window size          |
| temperature | float64 | Sampling temperature (0.0-2.0)       |
| threads     | int     | Number of CPU threads to use         |
| port        | int     | Port for llama.cpp server to listen  |
| mmproj      | string  | Path to mmproj file (optional)       |

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

### Run in Background (Daemon)

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
        "host": "127.0.0.1"
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
└── server/
    └── server.go       # HTTP server
```

### Dependencies

- **github.com/gorilla/mux**: HTTP router
- **github.com/spf13/viper**: Configuration management

## License

This is a demonstration project. Use at your own risk.

## Troubleshooting

### Server won't start
- Check llama.cpp is installed: `llama-cli --help`
- Verify model file exists and is readable
- Check the port is not already in use
- Review logs for errors

### Permission denied
- Ensure llama-cli is executable
- Check file permissions on model files

### Model won't load
- Verify model path is correct
- Ensure model file is in GGUF format
- Check available system memory
