import { get, writable } from 'svelte/store'
import { getRunning } from './api'
import type { Machine, MachineStatus, Settings } from './types'

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

/** Refresh one machine's running-model status. Never throws. */
export async function refreshMachine(m: Machine): Promise<void> {
  statuses.update((s) => (s[m.id] ? s : { ...s, [m.id]: { state: 'checking' } }))
  try {
    const running = await getRunning(m)
    statuses.update((s) => ({
      ...s,
      [m.id]: running
        ? { state: 'running', model: running.model_config.name, startTime: running.start_time }
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
  await Promise.allSettled(get(machines).map(refreshMachine))
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
