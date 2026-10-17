import { get, writable } from 'svelte/store'
import { ApiError, getFleet, getProxy, getRunning, getSystem, getWorkerSystem } from './api'
import type {
  FleetMachine,
  Machine,
  MachineStatus,
  Settings,
  SystemPoint,
  SystemSnapshot,
} from './types'

const MACHINES_KEY = 'llm-fleet.machines.v1'
const SETTINGS_KEY = 'llm-fleet.settings.v1'

export const DEFAULT_SETTINGS: Settings = { pollMs: 5000, paused: false, theme: 'light' }

function loadMachines(): Machine[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(MACHINES_KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

function loadSettings(): Settings {
  try {
    return { ...DEFAULT_SETTINGS, ...JSON.parse(localStorage.getItem(SETTINGS_KEY) ?? '{}') }
  } catch {
    return { ...DEFAULT_SETTINGS }
  }
}

/** localStorage writes can throw in Safari private mode or when storage is full. */
function persist(key: string, value: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // Storage unavailable/quota exceeded: keep the in-memory state working.
  }
}

/** Fleet registry, persisted to localStorage. */
export const machines = writable<Machine[]>(loadMachines())
machines.subscribe((v) => persist(MACHINES_KEY, v))

/** Poll interval / pause / theme, persisted to localStorage. */
export const settings = writable<Settings>(loadSettings())
settings.subscribe((v) => persist(SETTINGS_KEY, v))

/** Latest known status per machine id. */
export const statuses = writable<Record<string, MachineStatus>>({})

/**
 * Latest fleet view per machine id, from GET /api/v1/fleet. `null` means the
 * machine answered but has no fleet capability (legacy/standalone binary) —
 * those fall back to the single-card view. `undefined` = not probed yet.
 */
export const fleetViews = writable<Record<string, FleetMachine[] | null>>({})

/**
 * Runtime proxy state per machine id. `true`/`false` once known; `undefined`
 * while unknown or unsupported (older manager without GET /api/v1/proxy).
 */
export const proxies = writable<Record<string, boolean | undefined>>({})

/**
 * Latest host telemetry keyed by `systemKey(machineId, entryName)`. `null` =
 * the machine answered but does not support GET /api/v1/system (older binary)
 * or could not be reached; `undefined` = not sampled yet.
 */
export const systemStats = writable<Record<string, SystemSnapshot | null>>({})
export const systemErrors = writable<Record<string, string | undefined>>({})

/**
 * Client-side history for the live charts, one sample per poll, keyed like
 * systemStats. Each fleet entry (local + every worker) has its own series.
 */
export const systemHistory = writable<Record<string, SystemPoint[]>>({})

/** Enough samples for ~4 minutes at the default 5s interval. */
export const MAX_SYSTEM_POINTS = 48

/** Key for one fleet entry's telemetry ("local" or a worker name). */
export function systemKey(machineId: string, entry: string): string {
  return `${machineId}::${entry}`
}

/** One in-flight system request per entry, so slow polls cannot overlap. */
const systemInFlight = new Set<string>()

/** True only while the Fleet view is mounted: system stats are not sampled elsewhere. */
let telemetryViews = 0
let telemetryActive = false

/** Set by the Fleet view. Turning it off stops new telemetry samples immediately. */
export function setTelemetryActive(active: boolean): void {
  telemetryViews = Math.max(0, telemetryViews + (active ? 1 : -1))
  telemetryActive = telemetryViews > 0
}

/** True only while the Fleet view is the active route. */
let pollingActive = false

/** Set by the app router so background pages do not poll the fleet. */
export function setPollingActive(active: boolean): void {
  if (pollingActive === active) return
  pollingActive = active
  restartPolling()
}

/**
 * Refresh one fleet entry's host telemetry. Local entries are read from the
 * manager itself; workers are proxied through the coordinator so their API
 * keys never reach the browser. Never throws.
 */
export async function refreshSystem(m: Machine, entry = 'local'): Promise<void> {
  const key = systemKey(m.id, entry)
  if (systemInFlight.has(key)) return
  systemInFlight.add(key)
  try {
    const snap = entry === 'local' ? await getSystem(m) : await getWorkerSystem(m, entry)
    systemStats.update((s) => ({ ...s, [key]: snap }))
    systemErrors.update((s) => ({ ...s, [key]: undefined }))

    const point: SystemPoint = {
      cpu: snap.cpu.usage_percent,
      mem: snap.memory.usage_percent,
      gpu: snap.gpu?.available ? snap.gpu.usage_percent : null,
    }
    systemHistory.update((s) => {
      const next = [...(s[key] ?? []), point]
      return { ...s, [key]: next.slice(-MAX_SYSTEM_POINTS) }
    })
  } catch (e) {
    systemStats.update((s) => ({ ...s, [key]: null }))
    systemErrors.update((s) => ({
      ...s,
      [key]: e instanceof Error ? e.message : String(e),
    }))
  } finally {
    systemInFlight.delete(key)
  }
}

/** Sample every online entry; stats arrive independently of the fleet view. */
function refreshEntryStats(m: Machine, entries: FleetMachine[]): void {
  if (!telemetryActive) return
  for (const entry of entries) {
    if (entry.status === 'online') void refreshSystem(m, entry.machine)
  }
}

/** Refresh one manager's proxy state. Never throws. */
export async function refreshProxy(m: Machine): Promise<void> {
  try {
    const enabled = await getProxy(m)
    proxies.update((s) => ({ ...s, [m.id]: enabled ?? undefined }))
  } catch {
    proxies.update((s) => ({ ...s, [m.id]: undefined }))
  }
}

/**
 * Refresh only this manager's own running-model status. The Machine view uses
 * this instead of refreshMachine so opening a coordinator does not fan out to
 * every worker and collect fleet telemetry.
 */
export async function refreshMachineStatus(m: Machine): Promise<void> {
  try {
    const running = await getRunning(m)
    statuses.update((s) => ({
      ...s,
      [m.id]: running
        ? {
            state: 'running',
            model: running.model_config.alias || running.model_config.name,
            startTime: running.start_time,
          }
        : { state: 'idle' },
    }))
  } catch (e) {
    statuses.update((s) => ({
      ...s,
      [m.id]: { state: 'offline', error: e instanceof Error ? e.message : String(e) },
    }))
  }
}

/** Refresh one machine's fleet view and running-model status. Never throws. */
export async function refreshMachine(m: Machine): Promise<void> {
  statuses.update((s) => (s[m.id] ? s : { ...s, [m.id]: { state: 'checking' } }))

  try {
    const entries = await getFleet(m)
    // Fleet-capable: render the aggregated view and keep the plain status
    // fresh for the machine detail page.
    fleetViews.update((s) => ({ ...s, [m.id]: entries }))
    const local = entries.find((e) => e.machine === 'local')
    statuses.update((s) => ({
      ...s,
      [m.id]: local?.running
        ? { state: 'running', model: local.running.alias || local.running.name, startTime: local.running.start_time }
        : { state: 'idle' },
    }))
    refreshEntryStats(m, entries)
    return
  } catch (e) {
    fleetViews.update((s) => ({ ...s, [m.id]: null }))
    const apiErr = e instanceof ApiError ? e : null
    // 404/405: legacy binary without the fleet endpoints — fall through to
    // the legacy getRunning probe. Anything else (offline, auth, timeout) is
    // already the machine's state; surface it directly.
    if (!apiErr || (apiErr.status !== 404 && apiErr.status !== 405)) {
      statuses.update((s) => ({
        ...s,
        [m.id]: { state: 'offline', error: e instanceof Error ? e.message : String(e) },
      }))
      return
    }
  }

  try {
    const running = await getRunning(m)
    statuses.update((s) => ({
      ...s,
      [m.id]: running
        ? { state: 'running', model: running.model_config.alias || running.model_config.name, startTime: running.start_time }
        : { state: 'idle' },
    }))
    if (telemetryActive) void refreshSystem(m, 'local')
  } catch (e) {
    statuses.update((s) => ({
      ...s,
      [m.id]: { state: 'offline', error: e instanceof Error ? e.message : String(e) },
    }))
  }
}

/** Prevent a slow poll from overlapping the next interval tick. */
let refreshInFlight = false

/** Refresh every machine concurrently. */
export async function refreshAll(): Promise<void> {
  if (refreshInFlight) return
  refreshInFlight = true
  try {
    await Promise.allSettled(
      get(machines).flatMap((m) => [refreshMachine(m), refreshProxy(m)]),
    )
  } finally {
    refreshInFlight = false
  }
}

/** Machine id; crypto.randomUUID needs a secure context (localhost/https). */
export function newMachineId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `m-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/**
 * On first load, if no machines are configured, attach the manager that served
 * this UI as "This machine". It only seeds when the origin actually answers
 * like an llm_server_manager API, so a static host or the Vite dev server
 * (which return HTML) stay on manual setup. A key-protected manager still
 * answers with a JSON envelope (401), so it is seeded too — the user just adds
 * the key in Settings.
 */
export async function bootstrapLocalMachine(): Promise<void> {
  if (get(machines).length > 0) return
  const origin = typeof window !== 'undefined' ? window.location.origin : ''
  if (!origin || origin === 'null') return

  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), 3000)
  try {
    const res = await fetch(`${origin}/api/v1/models`, { signal: ctrl.signal })
    const body: unknown = await res.json().catch(() => null)
    if (!body || typeof body !== 'object' || !('success' in (body as object))) return
    if (get(machines).length > 0) return // the user added one meanwhile
    machines.set([{ id: newMachineId(), name: 'This machine', url: origin, apiKey: '' }])
  } catch {
    // Network error, timeout, or non-JSON body: not a manager, stay manual.
  } finally {
    clearTimeout(timer)
  }
}

let timer: ReturnType<typeof setInterval> | undefined

/** Auto-refresh runs only on the Fleet view, when visible and not paused. */
function shouldPoll(): boolean {
  if (!pollingActive) return false
  if (get(settings).paused) return false
  if (get(machines).length === 0) return false
  return typeof document === 'undefined' || !document.hidden
}

function restartPolling(): void {
  if (timer) clearInterval(timer)
  timer = undefined
  if (shouldPoll()) {
    timer = setInterval(refreshAll, get(settings).pollMs)
  }
}

// A hidden tab stops polling. When it comes back, refresh once and resume.
if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    restartPolling()
    if (shouldPoll()) void refreshAll()
  })
}

// Safari/Firefox may restore a page from the back/forward cache without a
// visibilitychange event. Treat a persisted pageshow like becoming visible.
if (typeof window !== 'undefined') {
  window.addEventListener('pageshow', (event) => {
    if (!event.persisted) return
    restartPolling()
    if (shouldPoll()) void refreshAll()
  })
}

settings.subscribe(restartPolling)
machines.subscribe(restartPolling)
