# Implementation log

Append-only record of implementation and review passes. New entries go at the
bottom; corrections to an old entry get a dated *(Superseded …)* note rather
than a silent rewrite.

## 2026-11-24 — LCARS console restyle (web UI)

**Scope:** visual restyle of `webui/` to the LCARS-inspired console theme in
`~/kunle_workspace/docs/mockups`, plus the associated docs and font assets. No
Go/API behaviour was changed by this work.

**Delivered**
- Warm paper console frame, recessed gradient screen, pastel command blocks,
  bottom command bar, instrument readout and stardate; both decorative side
  rails were dropped at the user's request.
- Self-hosted Saira / Saira Extra Condensed / IBM Plex Mono, bundled by Vite
  (no third-party font requests), with OFL notices in
  `webui/src/assets/fonts/`.
- Repeated LCARS card/row colours, command-pill buttons, metric readouts and
  charts, syntax-highlighted config editor, empty/loading/error states.
- Settings dialog reworked as an “access panel”: peach cap, sticky footer,
  colour ribbon, Escape close, initial focus, Tab containment and focus
  restoration.
- Restyled confirmation strips, conflict/external-change banners and toasts.
- Mobile layout: compact header, full-bleed screen, stacked model rows and
  metrics, scroll-contained dialogs, 16px form controls on coarse pointers to
  avoid iOS focus zoom, tested down to 320px.
- Docs refreshed (`README.md`, `AGENTS.md`, `docs/ui-proposal.md`) with an
  explicit implemented-deviation note for the original UI proposal.

**Self-review findings fixed before the adversarial pass**
1. The Fleet-only refresh/pause controls had leaked onto Machine/Config pages,
   where they duplicated the page-local refresh; moved them back under the
   Fleet-only condition.
2. Exposing the compact proxy-switch label left no gap between track and text;
   `.switch.compact` now keeps a 0.4rem gap.
3. The settings dialog had Escape/close but no Tab containment; added a focus
   trap (including recovery when the focused row is deleted) and focus restore.
4. The node count was derived twice (header and fleet kicker); extracted a
   single `fleetNodeCount` store so they cannot drift.
5. `a:focus-visible` changed `border-radius`, visibly deforming LCARS pills
   while focused; removed that declaration.
6. The header readout left an orphan `·` when the status text was hidden at
   medium widths; separator now hides with it.
7. The topbar refresh select used a sub-16px font, which triggers iOS focus
   zoom; added a `(pointer: coarse)` 16px rule (also covering phone landscape).
8. Internal screen scrolling kept the previous route's scroll offset; route
   changes now reset the screen to the top.
9. Dark-theme reloads flashed the light console until the theme effect ran;
   the persisted theme is now applied before mount in `main.ts`.
10. Added `dvh` fallbacks for the dialog max-height, font license notices, and
    removed dead CSS tokens/selectors and an unused import.

**Validation**
- `npm run check` — 0 errors, 0 warnings (after every review fix).
- `npm run build` — succeeds; fonts and hashed assets emitted.
- Clean-context Chromium Playwright: 1600 / 1024 / 768 / 700 / 480 / 390 / 320 px with
  no horizontal overflow, zero console errors, and the self-hosted faces loaded
  (Saira, IBM Plex Mono, and the loaded Saira Extra Condensed weight).
- Dialog: Escape close, focus restore, Tab/Shift+Tab containment, sticky footer
  and internal body scroll verified at 390px.
- External-change banner and `409` conflict banner verified (mobile), then
  reload-from-disk recovered cleanly.
- Proxy on/off switch and compact label verified; Fleet topbar shows the poll
  controls while Machine/Config show pause, theme and settings (pause/resume
  verified at 390px on both views).
- Chart surface restored (5% metric tint) so fresh/low-sample charts keep their
  extent without the earlier stripe/barcode effect.
- M1 repro: added an unreachable machine → `1 OFFLINE · 4 NODES`; removed it →
  `ALL SYSTEMS NOMINAL · 3 NODES`, with the stale status key still present in
  memory, proving the count is now registry-scoped.
- M2: dark offline telemetry panel now resolves to the same translucent panel
  tint as its card; the light slab and low-contrast text are gone.
- L4: simulated a `501` on the proxy config write; the checkbox snapped back to
  `checked`, the label stayed `Proxy on`, and the error toast was shown.
- L9: ran the UI against a stub legacy manager (`/fleet` → 404) whose running
  model has alias `atlas-chat` and real name `atlas-8b-instruct`; clicking Stop
  sent `DELETE /api/v1/models/atlas-8b-instruct/stop`, not the alias.
- Gecko (Firefox, `playwright-core`) at 390px and 320px with `pointer: coarse`:
  poll `<select>` and the settings import `<textarea>` compute to 16px, the
  dialog footer remains visible while its body scrolls, Escape closes, and there
  is no horizontal overflow; 0 console errors. WebKit could not launch on this
  host (missing `libavif16`), so it was not used as evidence.
- `go test ./...` — all packages pass.

**Deferred / pre-existing (not introduced by this diff, no Go files changed)**
- `go vet ./...` reports `metrics/sampler_linux.go:27: return copies lock value`.
- `go test -race ./...` reports a pre-existing data race between
  `manager.launchServer` writing `sm.server.Status` and a JSON handler reading
  the shared `RunningServer` returned by `GetCurrentServer`. (Both were present
  before this UI work; the UI does not import or change these packages.)

**Adversarial review (independent subagent, 2026-11-24)**
Counts: 0 CRITICAL, 0 HIGH, 2 MEDIUM, 9 LOW.

- **M1 (MEDIUM, deleted machines kept inflating the offline count) — confirmed,
  fixed.** Replaced the raw `statuses`/`fleetViews` sum with a
  `fleetOfflineCount` derived store that iterates `$machines`, so stale keys and
  late in-flight updates for deleted machines cannot count. Repro verified both
  directions (above).
- **M2 (MEDIUM, dark offline telemetry slab/contrast) — confirmed, fixed.**
  `.metrics-note` now uses `var(--panel-2)` instead of `rgba(255,255,255,.22)`.
  Dark screenshot and computed background verified.
- **L1 (LOW, pause unreachable on Machine/Config, especially mobile) — confirmed,
  fixed.** The pause/resume control is now in the topbar on every route (amber
  when paused); verified on Machine and Config at 390px.
- **L2 (LOW, Saira variable weight range wrong) — confirmed, fixed.** The
  shipped face is `wght 400–600` (verified with fontTools); the `@font-face`
  declaration and font README now say 400–600.
- **L3 (LOW, recessed-screen glow scrolled away) — confirmed, fixed.** Removed
  the absolute `::before` and moved the warm wash into the `.screen` background
  layers, which stay fixed to the scrollport; computed `background-image` and
  absence of `::before` verified.
- **L4 (LOW, proxy switch stayed visually flipped after a failed write) —
  confirmed, fixed.** The checkbox is explicitly set back to the authoritative
  store value in the `finally`; verified with a simulated `501`.
- **L5 (LOW, chart lost its track, empty tiles looked broken) — confirmed,
  fixed.** Kept transparent slots but gave `.chart` a 5% metric-tinted surface;
  fresh/low-sample charts keep their extent without slot stripes; computed
  background and screenshots verified.
- **L6 (LOW, settings import `textarea.code` still 12.8px on touch) — confirmed,
  fixed.** Added `textarea.code` to both the width and coarse-pointer 16px rules;
  computed 16px at 390px in Chromium and Gecko.
- **L7 (LOW, AGENTS token/inline-style inaccuracy) — confirmed, fixed.**
  `--ink` → `--text`/`--dim`; inline per-row custom properties are now documented.
- **L8 (LOW, AGENTS wordmark breakpoint 480 vs 520) — confirmed, fixed.**
- **L9 (LOW, legacy-card Stop sent the alias) — confirmed, fixed.** Added
  `MachineStatus.name` (real `ModelConfig.Name`) alongside the display
  `model`, populated it in all three status paths, and passed it to
  `doStopLocal`. Verified end-to-end with the legacy stub above.
- **Refuted:** none.
- **Extra bugs found during verification beyond the review list:** the route
  scroll reset and the pre-paint theme application (both in the self-review list
  above); no additional defects were found in the reviewer's focus areas.

**Artifacts**
- `dist/llm_server_manager-ui-darwin-arm64` — macOS arm64 with the UI embedded
  (`-tags embedui`).
- `dist/llm_server_manager-linux-arm64` — Linux arm64 without the embedded UI.
- Both are gitignored; the release binaries are built from the committed tree.
