# LLM Manager CLI

A command-line tool to interact with [LLM Server Manager](https://github.com/yourusername/llm_server_manager) via its REST API.

## Installation

```bash
# Build from source
cd tools/cli
go build -o llmcontrol .

# Move to PATH
mv llmcontrol /usr/local/bin/
```

## Usage

```bash
llmcontrol [options] <command> [arguments]
```

### Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--server` | `-s` | `http://localhost:8080` | LLM Server address |
| `--json` | `-j` | `false` | Output in JSON format |

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
$ llmcontrol list
NAME            MODEL PATH                        CONTEXT SIZE   TEMPERATURE   THREADS   ACTIVE
llama-7b        /models/llama-7b/llama-7b.gguf    4096           0.70          8         false
mistral-7b      /models/mistral-7b/mistral-7b.gguf   4096       0.70          8         true
```

With JSON output:
```bash
$ llmcontrol --json list
{"success":true,"data":{"models":[...]}}
```

### Start a model

```bash
$ llmcontrol start llama-7b
Server starting: model 'llama-7b' starting
```

### Stop a model

```bash
$ llmcontrol stop llama-7b
Stopped: model 'llama-7b' stopped successfully
```

### Check running status

```bash
$ llmcontrol status
model 'mistral-7b' is currently operating
```

Or check if any model is running:
```bash
$ llmcontrol running
```

### Connect to remote server

```bash
# Via command line flag
llmcontrol --server=http://192.168.1.100:8080 list

# Via environment variable
export LLM_MANAGER_URL="http://192.168.1.100:8080"
llmcontrol list
```

## Configuration

The CLI reads configuration from:

1. Command-line flags (highest priority)
2. Environment variable `LLM_MANAGER_URL`
3. Default: `http://localhost:8080`
