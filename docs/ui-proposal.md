# LLM Manager Fleet UI — Proposal

**Goal:** one simple, portable app to view and control `llm_server_manager` instances running on multiple machines.

**Status:** awaiting review — not started.

---

## 1. What it does

| # | Requirement | How |
|---|-------------|-----|
| 1 | See what model is running on each machine | Fleet dashboard; per-machine card shows the running model (or "idle"), polled every ~5s |
| 2 | Start / stop a model | Start from a machine's model list; stop with a one-click confirm from the card or detail view |
| 3 | *(optional)* Edit LLM configs and save | Config editor view per machine; saves via a **new** `PUT /api/v1/config` endpoint on the manager (see §3) |

Out of scope: logs streaming, metrics/charts, multi-user auth, model uploads.

---

## 2. Architecture

### Chosen: static Svelte SPA, browser talks directly to each manager

```
┌─────────── Browser (one page) ───────────┐
│  Svelte SPA  (static files, ~50KB gzip)  │
└────┬──────────────┬──────────────┬───────┘
     │ REST         │ REST         │ REST
     ▼              ▼              ▼
 Mac manager    Linux box      NAS manager
 :8080          :8080          :8080
 (existing API, unchanged for features 1–2)
```

- **No backend of its own.** The UI is a build output (`dist/`) — plain HTML/JS/CSS.
- Each machine is just a base URL + optional API key, stored in the browser (localStorage).
- Calls use the existing endpoints: `GET /models`, `GET /models/running`, `POST /models/{m}/start`, `DELETE /models/{m}/stop`, with the `api-key` header.
- Feature 1–2 need **zero server changes** (CORS already configurable via `LLM_ALLOWED_ORIGINS`).

**Trade-off to be aware of:** per-machine API keys live in the browser's localStorage. This is fine on a trusted LAN / personal machines. If that's unacceptable, see the alternative below.

### Alternative (documented, not chosen): tiny companion proxy

A ~100-line Go `fleet-proxy` command added to this repo: serves the static UI and reverse-proxies `/m/{id}/...` to configured machines, keeping keys server-side. Recommended only if you later need (a) keys hidden from the browser, or (b) a single HTTPS origin calling plain-HTTP machines (browsers block mixed content — a proxy is the only fix besides HTTPS everywhere).

---

## 3. Required backend additions (only for optional feature 3)

The manager currently has **no endpoint to read or write the config file**. The config editor therefore needs two small additions to `handlers` + `server.go`:

1. `GET /api/v1/config` → returns the raw JSON config (auth: same `api-key` middleware).
2. `PUT /api/v1/config` → validates JSON, writes the config file atomically, and lets the **existing fsnotify auto-reload** pick it up (no new reload logic — the watcher already debounces and rebuilds the model map safely; a running process is never restarted by a reload).

⚠️ **One constraint to change:** the global `http.MaxBytesReader` body limit is currently **1KB**. Any real config file exceeds that, so `PUT /config` must be exempted or the limit raised (e.g., 1MB for `/api/v1/config` only). This is a ~5-line middleware tweak.

If you skip feature 3, the UI ships with the editor view greyed out ("not supported by this manager version") and no backend changes are needed at all.

---

## 4. UI design

Three views, hash-routed (`#/fleet`, `#/machine/{id}`, `#/machine/{id}/config`). Dark theme, system fonts, no UI framework — hand-rolled CSS (~200 lines). All state client-side; polling via `setInterval` with a visible pause toggle.

### View 1 — Fleet dashboard (default)

```
┌──────────────────────────────────────────────────────────────┐
│ LLM Fleet                              ⟳ 5s ▸  ⏸  ⚙ Settings │
├──────────────────────────────────────────────────────────────┤
│ ┌─ mac-studio ────────────── ● running ──┐  ┌─ linux-box ──┐ │
│ │  Model: qwen-32b                       │  │ ● idle       │ │
│ │  qwen2.5-32b.gguf · port 8081          │  │ last: llama-8b│ │
│ │  [ Open ]            [ ■ Stop ]        │  │ [ Open ]     │ │
│ └────────────────────────────────────────┘  └──────────────┘ │
│ ┌─ nas ───────────────────── ✕ offline ──┐                   │
│ │  unreachable (timeout)                 │                   │
│ └────────────────────────────────────────┘                   │
└──────────────────────────────────────────────────────────────┘
```

- Status dot: ● running / ○ idle / ✕ offline (timeout or 401). Card layout auto-wraps; readable on phone.
- **Stop is the only destructive action** → inline confirm ("Stop qwen-32b? [Yes] [No]"), never a browser alert.
- ⚙ Settings: add/edit/remove machines (name, base URL, API key), import/export fleet as JSON.

### View 2 — Machine detail

```
┌──────────────────────────────────────────────┐
│ ← Fleet   mac-studio ● running qwen-32b      │
├──────────────────────────────────────────────┤
│ RUNNING NOW                                  │
│  qwen-32b · started 14:32 · port 8081  [■Stop]│
├──────────────────────────────────────────────┤
│ MODELS                    [ Edit config → ]  │
│  ● qwen-32b     running        [ ■ Stop ]    │
│  ○ llama-8b     idle           [ ▶ Start ]   │
│  ○ phi-4-mini   idle           [ ▶ Start ]   │
│  (start disabled while another model runs —  │
│   manager enforces one server at a time)     │
└──────────────────────────────────────────────┘
```

- Merges `GET /models` + `GET /models/running` into one list.
- Start button shows a spinner until the request returns; errors surface as a toast (e.g., "model file not found" from the manager's message).

### View 3 — Config editor (optional feature)

```
┌──────────────────────────────────────────────┐
│ ← mac-studio   Config   (raw JSON)           │
├──────────────────────────────────────────────┤
│ ┌──────────────────────────────────────────┐ │
│ │ { "models": [ ... ] }     (monospace,    │ │
│ │                          line numbers)   │ │
│ └──────────────────────────────────────────┘ │
│  ✓ Valid JSON        Revert ↺   Save 💾      │
└──────────────────────────────────────────────┘
```

- Deliberately **raw JSON, not a form editor**: the schema already has optional/odd fields (`launch_cmd` overrides everything, `spec-draft-n-max`, etc.). A form would be a larger, buggier surface; JSON + validation covers 100% of the schema.
- Client-side JSON validation before Save is enabled; server errors (from validation on `PUT`) shown inline.
- Save → `PUT /api/v1/config` → toast "Saved, config reloaded".

---

## 5. Tech stack & repo layout

| Choice | Rationale |
|--------|-----------|
| **Svelte 5 + Vite + TypeScript** | Per your preference; tiny output (~50KB gzip total, no runtime UI lib) |
| Hash routing (hand-rolled, ~30 lines) | Works from any static host or subdirectory, no server rewrites needed |
| Hand-rolled CSS, dark theme | No Tailwind/Melt-ui dependency; consistent look; trivial to theme later |
| `fetch` + `AbortController` (3s timeout per machine) | Offline machines detected fast; no axios |
| No state library | App state is ~4 values + machine list; Svelte stores suffice |

```
webui/
├── package.json  vite.config.ts  tsconfig.json  svelte.config.js
├── src/
│   ├── main.ts  App.svelte
│   ├── lib/
│   │   ├── api.ts          # typed client for the 4+2 endpoints, per-machine key header
│   │   ├── fleet.ts        # machine registry (localStorage), poller store
│   │   └── toast.ts
│   ├── views/
│   │   ├── Fleet.svelte  Machine.svelte  ConfigEditor.svelte  Settings.svelte
│   └── app.css
└── dist/                   # build output = the whole app
```

Lives in `webui/` inside this repo so the API client and the Go handlers evolve together.

---

## 6. Portability / deployment options (pick any, same build)

1. **Served by the manager itself** (recommended): embed `dist/` in the Go binary (Go 1.16 `embed`) behind a `-ui` flag → one binary per machine, UI at `http://machine:8080/ui`. ~30 lines in `server.go`.
2. **Any static host**: GitHub Pages, nginx, a USB stick (`python -m http.server`). Point it at remote managers.
3. **Run locally**: `npm run dev` while developing.

Fleet definitions are per-browser; "Export fleet JSON" makes moving between browsers/machines a copy-paste.

---

## 7. Security notes

- API keys sent as `api-key` header over the LAN; stored in localStorage (accepted trade-off, §2).
- CORS: when the UI is hosted on origin X and calls manager on origin Y, that manager must list X in `LLM_ALLOWED_ORIGINS`. Documented in the README; without it, requests fail visibly (offline/401 dot) rather than silently.
- All writes (start/stop/save) require explicit user action; no auto-retry on mutations.

---

## 8. Implementation plan

| Phase | Contents | Server changes |
|-------|----------|----------------|
| **1** | Scaffold, fleet store, Fleet view (status + running model), start/stop with confirm, settings modal | **None** |
| **2** | Machine detail view, toasts, polling controls, import/export | **None** |
| **3** *(optional)* | Config editor view | `GET/PUT /api/v1/config` + body-limit exemption (~60 lines Go + tests) |
| **4** *(optional)* | Embedded UI in the Go binary (`-ui` flag) | ~30 lines Go |

Estimate: Phase 1–2 ≈ one focused session; Phase 3 adds a Go endpoint + tests.

---

## 9. Questions for you (answer inline or just say "go with defaults")

1. **Keys in browser OK?** (LAN use) — default: yes; proxy alternative exists if not.
2. **Feature 3 in scope now?** — default: yes, including the two new Go endpoints.
3. **Embed UI in the Go binary (Phase 4)?** — default: yes, it's the most portable option.
4. **Theme** — default: dark-only (matches llama.cpp CLI vibe); light theme trivial to add later.
