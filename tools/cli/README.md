# LLM Manager CLI

A command-line tool to interact with [LLM Server Manager](https://github.com/yourusername/llm_server_manager) via its REST API.

## Installation

```bash
# Build from source
cd tools/cli
go build -o llm-cli .

# Move to PATH
mv llm-cli /usr/local/bin/
```

## Usage

```bash
llm-cli [options] <command> [arguments]
```

### Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--server` | `-s` | `http://localhost:8080` | LLM Server address |
| `--json` | `-j` | `false` | Output in JSON format |
| `--skip-tls-verify` | - | `false` | Skip TLS certificate verification for HTTPS connections |

### Commands

| Command | Aliases | Description |
|---------|---------|-------------|
| `list` | `ls`, `models` | List all configured models |
| `start <model>` | - | Start a model server |
| `stop <model>` | - | Stop a running model server |
| `status` | `running`, `info` | Show the current running model |
| `version` | - | Show CLI version |

## Examples

### List all configured models

```bash
$ llm-cli list
NAME            MODEL PATH                        CONTEXT SIZE   TEMPERATURE   THREADS   ACTIVE
llama-7b        /models/llama-7b/llama-7b.gguf    4096           0.70          8         false
mistral-7b      /models/mistral-7b/mistral-7b.gguf   4096       0.70          8         true
```

With JSON output:
```bash
$ llm-cli --json list
{"success":true,"data":{"models":[...]}}
```

### Start a model

```bash
$ llm-cli start llama-7b
Server starting: model 'llama-7b' starting
```

### Stop a model

```bash
$ llm-cli stop llama-7b
Stopped: model 'llama-7b' stopped successfully
```

### Check running status

```bash
$ llm-cli status
model 'mistral-7b' is currently operating
```

Or check if any model is running:
```bash
$ llm-cli running
```

### Connect to remote server

```bash
# Via command line flag
llm-cli --server=http://192.168.1.100:8080 list

# Via environment variable
export LLAMA_SERVER_URL="http://192.168.1.100:8080"
llm-cli list
```

### Connect via HTTPS

```bash
# Standard HTTPS (valid certificates)
llm-cli --server=https://192.168.1.100:8080 list

# Self-signed certificates (skip TLS verification)
llm-cli --server=https://192.168.1.100:8080 --skip-tls-verify list
```

## Authentication

When the server is configured with API key authentication, set the `LLM_MANAGER_API_KEY` environment variable:

```bash
export LLM_MANAGER_API_KEY="abcdef1234567890"
llm-cli list
```

The API key must be exactly 16 alphanumeric characters.

## Configuration

The CLI reads configuration in the following priority:

1. Command-line flags (highest priority)
2. Environment variable `LLAMA_SERVER_URL`
3. Default: `http://localhost:8080`

For authentication:
- Environment variable `LLM_MANAGER_API_KEY` (must be 16 alphanumeric characters)
