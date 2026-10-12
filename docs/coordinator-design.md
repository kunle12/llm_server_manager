# Coordinator / Worker Architecture + OpenAI Proxy — Design

**Status:** approved — ready for implementation.
**Scope:** extends the architecture in `AGENTS.md`; the web UI is described in `docs/ui-proposal.md` (shipped).

---

## 1. Overview

An llm_server_manager instance gains two opt-in capabilities. Everything is backward compatible: with neither feature enabled, the binary behaves exactly as today.

1. **Coordinator/worker mode.** An instance configured with a `workers` list becomes a *coordinator*: it lists, starts, and stops models on remote llm_server_manager instances through their existing REST API. Instances without `workers` remain *standalone* — unchanged. **Workers run today's binary with zero modifications.**
2. **OpenAI-compatible proxy router.** With `proxy: true`, the coordinator exposes `/v1/...` endpoints and routes each request to the machine (local or worker) that is currently running the requested model. Streaming (SSE) is fully supported.
3. **Model aliases.** Any model may declare an optional `alias`; when the proxy is on, the model is published and addressable under that alias, falling back to its real name.

**Non-goals (v1):** hierarchical fleets (coordinators of coordinators); load-balancing across multiple machines running the same model beyond first-match; per-client quotas; response-body rewriting; web UI/CLI changes before Phase 4.

---

## 2. Foundations

Design decisions rest on these verified facts:

| Fact | Consequence |
|---|---|
| llama-server natively serves `POST /v1/chat/completions`, `POST /v1/completions`, `POST /v1/embeddings`, `GET /v1/models`, `GET /health` | The proxy is a **thin router**, not an API reimplementation: extract `model`, find who runs it, reverse-proxy the bytes |
| OpenAI streaming is SSE: `data: {...}` chunks, terminated by `data: [DONE]`; `stream_options.include_usage` adds a final usage chunk | A streaming-safe reverse proxy (no buffering, immediate flush) satisfies the spec |
| The manager REST API already provides everything: `GET /models` (incl. `port`), `GET /models/running`, `POST .../start`, `DELETE .../stop`, `api-key` auth | Workers need no changes |
| llama-server's port defaults to **8081** when unset; it binds `0.0.0.0` | Routing target = `http://<host>:<port>`; the coordinator needs network reachability to worker **LLM ports**, not just manager ports |
| `manager` flips a model's status to `running` the moment the process spawns — *before* weights finish loading | "Running" ≠ "ready"; llama-server's own 503 "loading model" responses are the readiness signal and pass through |
| Current HTTP server sets `ReadTimeout`/`WriteTimeout` = 15s; request bodies are capped at 1KB (1MB for the config endpoint) | Both must be reworked for the proxy (§7.3) — the 15s `WriteTimeout` would truncate every generation |
| Config auto-reload (fsnotify, 300ms debounce) already rebuilds model state | Worker registry and routing state hook into the same reload path; running processes are never disturbed |
| Rate limiter: 10 req/s per IP, burst 20 | LLM traffic is few-large-requests; acceptable in v1, tunable later |

---

## 3. Configuration

```json
{
  "models": [
    {
      "name": "qwen-32b",
      "alias": "gpt-4o",
      "model_path": "/models/qwen.gguf",
      "context_size": 8192,
      "threads": 8,
      "port": 8081
    }
  ],

  "workers": [
    "http://gpu1.local:8080",
    { "url": "http://gpu2.local:8080", "name": "gpu2", "api_key": "abcdef1234567890" }
  ],

  "proxy": true
}
```

**Mode detection.** `workers` absent or empty ⇒ standalone (exact current behavior). Non-empty ⇒ coordinator. Decided at load and re-derived on every config reload through the existing watcher, so editing the config switches modes live without restarting the process.

**`workers` entries** — two accepted forms:
- string: `"http://host:8080"` — no per-worker auth is sent.
- object: `{ "url": "...", "name"?: "...", "api_key"?: "..." }` — `name` defaults to the URL host.
- Entries without a parseable URL are skipped with a warning (mirroring the nameless-model rule).

**`proxy`** — boolean, default `false`. The parser tolerates an object form for forward compatibility, but v1 defines no object keys. The proxy is valid in standalone mode too (it would route to the local llama-server only).

**`alias`** — optional, non-empty string when present. Duplicate aliases *within one config file* produce a load-time warning (not a rejection); an alias colliding with another machine's real model name warns when the routing table is built. Existing validation gates (name, model_path, threads, temperature) apply unchanged.

---

## 4. Security & API keys

Three trust relationships:

```
 client ──(A)──► coordinator ──(B)──► worker manager API
 client ──(A)──► coordinator ──(C)──► worker llama-server :port
```

**(A) Client → coordinator** — unchanged mechanism: `LLM_MANAGER_API_KEY` via the `api-key` header, constant-time compare. On `/v1/*` routes the same key is additionally accepted as `Authorization: Bearer <key>`, because that is what OpenAI clients send. If no key is configured, auth stays disabled as today (warning logged).

**(B) Coordinator → worker manager API** — per-worker `api_key` from the coordinator's config, sent as the worker's `api-key` header. Workers keep authenticating with their own `LLM_MANAGER_API_KEY`; **no worker-side change**. Entries without `api_key` are called unauthenticated (works with auth-disabled workers); 401s surface clearly in fleet status.

**(C) Coordinator → worker llama-server** — llama-server runs keyless today, so no upstream auth. Known gap; a future `upstream_api_key` config could present a shared llama-server `--api-key`.

**Handling of secrets:**
- The config file may contain worker keys — `chmod 600` recommended; keys are never logged.
- `GET /api/v1/config` returns the raw file (secrets included) and is protected by the same (A) auth as today.
- Fleet/worker endpoints never echo keys — redacted to `"set"` / `""`.
- Per-worker keys (vs. one shared fleet key) allow per-worker revocation; setting the same key everywhere remains a valid pattern.

**Other:** `/v1/*` inherits the global CORS middleware (lock down via `LLM_ALLOWED_ORIGINS` for browser clients). Anyone with the coordinator key can generate against any routed model — intended. Proxy targets come exclusively from config, never from user input (no SSRF surface).

---

## 5. Architecture

```
                        ┌──────────────────────────── coordinator ────────────────────────────┐
 OpenAI clients ──(A)──►│  /v1/* proxy ─── routing table ──► local llama-server :8081          │
 (streaming OK)         │       │                                                              │
                        │       └──────────────────────────► worker llama-server :8081         │
 admin UI / CLI ──(A)──►│  /api/v1/*  ── local manager ──► local llama.cpp proc                │
                        │       │                                                              │
                        │       └── worker client (B) ──► worker manager /api/v1/*             │
                        └──────────────────────────────────────────────────────────────────────┘
```

Two new packages, wired in `server`/`handlers`; `manager` is untouched:

- **`fleet`** — worker registry, HTTP client (auth header, timeouts), health cache.
- **`proxy`** — routing table, `/v1` handlers, reverse-proxy wiring, OpenAI-format errors.

The coordinator's **local models remain first-class**: the local manager still starts/stops local models, and the local machine participates in the fleet as machine `"local"`.

---

## 6. Control-plane API (coordinator mode)

All under auth (A); responses use the existing `APIResponse` envelope.

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/workers` | Worker list with health: `[{name, url, status: "online"\|"offline", running_model, model_count, error?}]` (`api_key` redacted) |
| `GET /api/v1/fleet` | Aggregated view, local machine first: `[{machine, url, status, running: {name, since, port} \| null, models: [ModelItem…]}]` — one call feeds the whole UI fleet view |
| `GET /api/v1/workers/{worker}/models` | Passthrough of a worker's model list |
| `POST /api/v1/workers/{worker}/models/{model}/start` | Start on that worker; success/error body passed through |
| `DELETE /api/v1/workers/{worker}/models/{model}/stop` | Stop on that worker |

Semantics:
- The `{worker}` path segment matches the configured `name` or the URL host.
- Worker call timeouts: 5s for reads, 30s for start/stop (model spawn takes a moment).
- Offline workers appear as `status: "offline"` in list/fleet responses instead of failing the whole request.
- **Fleet-level "start anywhere" is client-side:** the UI/CLI picks a machine from `GET /fleet` (model configured + machine free) and calls the worker-scoped start. No dedicated endpoint.
- `/api/v1/*` continues to use **real model names exclusively** (start/stop are not aliased); `alias` is included in payloads for display.

---

## 7. Proxy router

### 7.1 Endpoints

| Route | Behavior |
|---|---|
| `GET /v1/models` | OpenAI-format `{"object":"list","data":[...]}`; publishes **only models currently running** on some machine, named by alias when set, else real name (§8) |
| `POST /v1/chat/completions` | Parse body, extract `model`, resolve target (§7.2), reverse-proxy |
| `POST /v1/completions` | Same |
| `POST /v1/embeddings` | Same |
| any other `/v1/*` | 404 in OpenAI error format |

### 7.2 Routing table

State: `map<publishedName, []target>` where target = `{machine, base_url}`; `base_url` = `http://<host>:<port>`. Host is the worker URL's host, or `127.0.0.1` for the local machine. Port is the model's configured `port`, defaulting to 8081.

- **Sources:** local manager state (`ListModels` / `GetCurrentServer`) plus each worker's `GET /models` and `GET /models/running`.
- **Published name:** `alias` if set, else `name` (§8).
- **Freshness:** 2s TTL cache; a request for an unknown name triggers one synchronous refresh (single-flight per name) before answering 404 — low steady-state latency, self-correcting after fleet changes.
- **First-match order** when several machines run the same published name: local, then workers in config order. (Round-robin is future work.)
- **Routability:** a model is routable while its machine reports it `running`. A machine reporting `starting` is treated as *warming* (§7.4). Note that `running` means "process spawned" — weight loading is covered by upstream 503 pass-through (§7.4).
- **`launch_cmd` models — port discovery:** the manager API reports no port for raw `launch_cmd` models, so the coordinator parses the command string: a match for `--port XXXX` (also `--port=XXXX`; first match wins; port must be 1–65535) makes the service routable at `host:XXXX` once running. **No match → the model is controllable via the control plane but never published or routed on `/v1`.** Assumes the process binds its llama port on the manager's host (same assumption as standard models).
- **Stale targets:** if an upstream refuses the connection or resets immediately (model just stopped/crashed while the cache still held it), the coordinator invalidates the cache entry, re-resolves once, and only then returns 502/404.

### 7.3 Streaming & HTTP server changes (critical)

- Forwarding uses `httputil.ReverseProxy` with `FlushInterval: -1` (flush after every write) — correct for SSE, harmless for JSON. Client disconnects propagate automatically via request-context cancellation.
- **Server timeouts rework:** `ReadHeaderTimeout: 10s` (keeps slowloris protection on headers), `ReadTimeout: 0`, `WriteTimeout: 0`, `IdleTimeout: 60s`. The old `WriteTimeout: 15s` would truncate every long generation; control handlers are fast and remain guarded by body caps and the rate limiter.
- **Body limits:** `/v1/*` is exempt from the 1KB rule and capped at 10MB (real prompts exceed 1KB); `/api/v1/config` keeps 1MB; everything else stays 1KB.
- **Request bodies are buffered** (up to the 10MB cap, `Content-Length` preserved): choosing a route requires reading the JSON body to extract `model`. **Response bodies are never buffered** — SSE must flow through untouched. Worst case is 10MB per in-flight request; acceptable, revisited if multimodal payloads grow.

### 7.4 Errors — OpenAI format on `/v1/*`

```json
{ "error": { "message": "model 'gpt-4o' is not running on any machine", "type": "invalid_request_error", "code": "model_not_available" } }
```

| Situation | Response |
|---|---|
| Unknown or unroutable model | 404 `model_not_available`. A configured-but-idle model becomes reachable only after an explicit start via the control plane (API/UI/CLI) — matching the manager's explicit-start philosophy |
| Machine reports the model `starting` (warming) | 503 with `Retry-After: 5` rather than a misleading 404 |
| Model process up, weights still loading | llama-server itself answers 503 "loading model"; passed through verbatim — the natural OpenAI-compatible readiness signal |
| Worker / llama-server unreachable | 502 `api_error`; upstream detail logged, generic message returned |
| Upstream 4xx/5xx | Passed through verbatim (llama-server already returns OpenAI-style errors) |
| Malformed JSON body | 400 `invalid_request_error` |

### 7.5 Response fidelity

Bodies pass through untouched, **including the `model` field** — llama-server reports the real model name even when the request used an alias. Rationale: rewriting streamed chunks would require SSE-aware body mutation (fragile, breaks the no-response-buffering property), and discovery/addressing via alias is what clients actually rely on. The `model` echo field is informational.

---

## 8. Model aliases

- **Config:** `"alias": "gpt-4o"` — optional per model; falls back to the real name when unset. Adding the field to `ModelConfig` makes it flow through existing `ModelItem` JSON automatically, so UI/CLI can display it for free.
- **Publishing (`GET /v1/models`):** each running model appears once, under its alias when set, else its real name. Payloads stay exactly OpenAI-shaped (`id`, `object`, `created`, `owned_by`) — no extra fields.
- **Addressing:** the `model` field in `/v1/*` requests matches alias first, then real name — both always work, so scripts using real names keep working when an alias is added.
- **Collisions:** duplicate aliases in one config file → load-time warning; cross-machine collisions → routing-table-build warning; both resolve by first-match (local, then `workers` order).
- **Control plane:** real names only (§6).

---

## 9. Failure modes

| Case | Behavior |
|---|---|
| Worker offline | Fleet/list shows `offline` + error; proxy skips it when routing; start/stop returns a 502-style message |
| Worker 401 | Fleet entry shows an `unauthorized` hint (clear misconfiguration signal) |
| Model crashes mid-stream | Upstream connection drops; client sees a truncated stream (standard); stale cached target is detected (connection refused) and re-resolved once before erroring |
| Model just started, weights loading | Machine reports `running` once the process spawns; requests are forwarded and llama-server's 503 "loading model" passes through until resident |
| Model stopped while cache warm | Stale-target retry (§7.2) turns a would-be 502 into a clean re-resolution / 404 |
| Config reload while proxying | Routing table rebuilt; in-flight requests unaffected (each holds its own resolved target) |
| Coordinator restarted | Stateless — table rebuilds lazily from workers on demand |
| Same model on local + worker | Local preferred; each machine runs at most one model; both may run simultaneously on different machines (first-match routing) |
| Worker LLM port unreachable (firewall) | 502 on proxied requests; control ops still work; documented network requirement |

---

## 10. Impact on existing code

| Piece | Change |
|---|---|
| `models.ModelConfig` | Add `Alias string` (+ `json`/`mapstructure` tags) |
| `config` | Parse `workers` (string or object entries) and `proxy` (bool, object tolerated); validation warnings |
| `manager` | None — existing methods suffice |
| new `fleet` package | `Worker` type, HTTP client (auth header, timeouts), registry with reload, health cache |
| new `proxy` package | Routing table, `/v1` handlers, ReverseProxy wiring, OpenAI-format errors |
| `server` | Route registration, timeout/body-limit rework, mode wiring, reload hooks |
| `handlers` | New fleet/worker endpoints; `/v1` delegated to the proxy package |
| CLI (Phase 4) | `llm-cli fleet`, `llm-cli start <model> --worker <name>`, `llm-cli status --fleet` |
| Web UI (Phase 4) | Fleet view prefers `GET /api/v1/fleet` when the target answers it (capability probe), falls back to per-machine polling; alias shown as display name |

---

## 11. Implementation plan

| Phase | Contents | Tests | Est. |
|---|---|---|---|
| **1 — Fleet foundation** | `ModelConfig.Alias`; `workers`/`proxy` config parsing + validation; `fleet` package (client, registry, health cache); reload integration; `GET /workers`, `GET /fleet`, worker-scoped start/stop | fake workers via `httptest` (online/offline/401); reload adds/removes workers; race tests | ~1 session |
| **2 — Proxy core** | Server timeout/body-limit rework; `/v1` routes with Bearer auth; routing table + TTL cache + single-flight + stale retry; ReverseProxy passthrough; **streaming verified** (SSE chunk timing, `[DONE]` pass-through, client-cancel propagation); OpenAI-format errors | fake llama upstreams (`httptest` SSE); unknown-model 404; warming 503; 502 path; body-limit matrix | ~1 session |
| **3 — Aliases** | Alias publishing/addressing in the routing table; `/v1/models` aggregation | alias fallback; duplicate-alias warnings; alias-collides-with-real-name warning | ~1 session |
| **4 — Client surfacing** | CLI fleet commands; web UI coordinator support (`/fleet` capability probe, alias display, client-side "start anywhere") | manual + UI smoke | ~1 session |

**E2E validation (Phases 2–3):** two in-process managers (coordinator + fake worker) plus a fake llama-server streaming 20 SSE chunks with 50ms gaps. Assert: first-chunk latency < 200ms, all chunks arrive, `[DONE]` present, client cancellation closes the upstream request.

**Compatibility guarantee:** with no `workers` key and `proxy` unset, observable behavior is identical to today except the timeout rework (§7.3), which only loosens limits. All existing tests must pass unmodified (plus additions).

---

## Appendix A — Decision log

| # | Decision | Origin |
|---|---|---|
| 1 | Worker entries: plain URL strings and `{url, name, api_key}` objects both accepted | User review |
| 2 | `proxy` is a plain bool; object form tolerated but undefined in v1 | User review |
| 3 | Autostart (request-driven model start) dropped — `/v1` routes only what machines already run | User review |
| 4 | `GET /v1/models` publishes only currently-alive models | User review |
| 5 | `launch_cmd` models routable when `--port XXXX` parses from the command string; otherwise ignored | User review |
| 6 | Response `model` field: pass-through everywhere, including SSE chunks | User review |
| 7 | Fleet-level start placement handled client-side via `/fleet` (no dedicated endpoint) | Review |
| 8 | `launch_cmd` port gap, buffering contradiction, "running ≠ ready", stale-route window, slowloris trade-off, alias cross-collision warnings — all found in self-review and folded into §7/§9 | Self-review |

## Appendix B — Considered and dropped

- **Autostart:** auto-starting a configured-but-idle model on a free machine when a `/v1` request names it. The full analysis (implicit placement affinity, idle detection via `models/running`, start-failure failover, 409-vs-404 error distinction) is in git history; reviving it would add `proxy.autostart`, a readiness wait, and single-flight placement.
- **Auto-placement start endpoint:** superseded by client-side placement over `GET /fleet` (§6).
