import { get, writable } from 'svelte/store'
import { ApiError, getFleet, getProxy, getRunning } from './api'
import type { FleetMachine, Machine, MachineStatus, Settings } from './types'

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

/** Fleet registry, persisted to localStorage. */
export const machines = writable<Machine[]>(loadMachines())
machines.subscribe((v) => localStorage.setItem(MACHINES_KEY, JSON.stringify(v)))

/** Poll interval / pause / theme, persisted to localStorage. */
export const settings = writable<Settings>(loadSettings())
settings.subscribe((v) => localStorage.setItem(SETTINGS_KEY, JSON.stringify(v)))

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

/** Refresh one machine's proxy state. Never throws. */
export async function refreshProxy(m: Machine): Promise<void> {
  try {
    const enabled = await getProxy(m)
    proxies.update((s) => ({ ...s, [m.id]: enabled ?? undefined }))
  } catch {
    proxies.update((s) => ({ ...s, [m.id]: undefined }))
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
  } catch (e) {
    statuses.update((s) => ({
      ...s,
      [m.id]: { state: 'offline', error: e instanceof Error ? e.message : String(e) },
    }))
  }
}

/** Refresh every machine concurrently. */
export async function refreshAll(): Promise<void> {
  await Promise.allSettled(
    get(machines).flatMap((m) => [refreshMachine(m), refreshProxy(m)]),
  )
}

/** Machine id; crypto.randomUUID needs a secure context (localhost/https). */
function machineId(): string {
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
    machines.set([{ id: machineId(), name: 'This machine', url: origin, apiKey: '' }])
  } catch {
    // Network error, timeout, or non-JSON body: not a manager, stay manual.
  } finally {
    clearTimeout(timer)
  }
}

let timer: ReturnType<typeof setInterval> | undefined

function restartPolling(): void {
  if (timer) clearInterval(timer)
  timer = undefined
  const s = get(settings)
  if (!s.paused && get(machines).length > 0) {
    timer = setInterval(refreshAll, s.pollMs)
  }
}

settings.subscribe(restartPolling)
machines.subscribe(restartPolling)
