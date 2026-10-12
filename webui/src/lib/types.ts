/** Types mirroring the llm_server_manager REST API. */

export interface ModelConfig {
  name: string
  alias?: string
  model_path?: string
  context_size?: number
  temperature?: number
  top_k?: number
  top_p?: number
  presence_penalty?: number
  repetition_penalty?: number
  threads?: number
  port?: number
  mmproj?: string
  chat_template_kwargs?: string
  ngl?: number
  mmap?: boolean
  'spec-draft-n-max'?: number
  launch_cmd?: string
}

export interface ModelItem extends ModelConfig {
  active: boolean
}

export interface RunningServer {
  model_config: ModelConfig
  pid: number
  status: 'starting' | 'running' | 'stopping'
  start_time: string
  crash_count: number
}

export interface APIResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

/** One llm_server_manager instance the UI talks to. */
export interface Machine {
  id: string
  name: string
  url: string
  apiKey: string
}

export type MachineState = 'checking' | 'running' | 'idle' | 'offline'

export interface MachineStatus {
  state: MachineState
  model?: string
  startTime?: string
  error?: string
}

/** UI-level settings (polling, theme), persisted to localStorage. */
export interface Settings {
  pollMs: number
  paused: boolean
  theme: 'light' | 'dark'
}

/** A model a machine is currently running, as reported by the fleet view. */
export interface RunningInfo {
  name: string
  alias?: string
  status: string
  start_time?: string
  port: number
}

/**
 * One machine in a coordinator's fleet view: the coordinator itself
 * ("local") first, then its workers. Only fleet-capable managers answer this.
 */
export interface FleetMachine {
  machine: string
  url?: string
  status: 'online' | 'offline'
  running?: RunningInfo | null
  models: ModelItem[]
  error?: string
}
