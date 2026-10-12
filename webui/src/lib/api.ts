import type { APIResponse, FleetMachine, Machine, ModelItem, RunningServer } from './types'

export class ApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}

const READ_TIMEOUT = 5000
const WRITE_TIMEOUT = 30000

function url(m: Machine, path: string): string {
  return m.url.replace(/\/+$/, '') + path
}

function wrapError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  if (e instanceof DOMException && e.name === 'AbortError') return new ApiError('timeout', 0)
  return new ApiError(e instanceof Error ? e.message : String(e), 0)
}

/** JSON API call; resolves with the APIResponse envelope or throws ApiError. */
async function request(
  m: Machine,
  path: string,
  init: RequestInit & { timeout?: number } = {},
): Promise<APIResponse> {
  const { timeout = READ_TIMEOUT, ...rest } = init
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeout)
  try {
    const res = await fetch(url(m, path), {
      ...rest,
      headers: { 'api-key': m.apiKey, ...(rest.headers as Record<string, string> | undefined) },
      signal: ctrl.signal,
    })
    let body: APIResponse | null = null
    try {
      body = await res.json()
    } catch {
      // non-JSON body (proxy error page etc.)
    }
    if (!res.ok) throw new ApiError(body?.message ?? `HTTP ${res.status}`, res.status)
    if (!body) throw new ApiError(`unexpected response (HTTP ${res.status})`, res.status)
    return body
  } catch (e) {
    throw wrapError(e)
  } finally {
    clearTimeout(timer)
  }
}

/** GET /api/v1/models/running — null means the manager answered but is idle. */
export async function getRunning(m: Machine): Promise<RunningServer | null> {
  const body = await request(m, '/api/v1/models/running')
  if (body.success && body.data) return body.data as RunningServer
  return null
}

/** GET /api/v1/models */
export async function listModels(m: Machine): Promise<ModelItem[]> {
  const body = await request(m, '/api/v1/models')
  const data = body.data as { models?: ModelItem[] } | undefined
  return data?.models ?? []
}

/**
 * GET /api/v1/fleet — the coordinator's aggregated view (local machine first,
 * then workers). 404s (legacy standalone binary) surface as ApiError so
 * callers can fall back to per-machine polling.
 */
export async function getFleet(m: Machine): Promise<FleetMachine[]> {
  const body = await request(m, '/api/v1/fleet')
  const data = body.data as { machines?: FleetMachine[] } | undefined
  return data?.machines ?? []
}

/** POST /api/v1/workers/{worker}/models/{name}/start */
export async function workerStartModel(m: Machine, worker: string, name: string): Promise<APIResponse> {
  return request(m, `/api/v1/workers/${encodeURIComponent(worker)}/models/${encodeURIComponent(name)}/start`, {
    method: 'POST',
    timeout: WRITE_TIMEOUT,
  })
}

/** DELETE /api/v1/workers/{worker}/models/{name}/stop */
export async function workerStopModel(m: Machine, worker: string, name: string): Promise<APIResponse> {
  return request(m, `/api/v1/workers/${encodeURIComponent(worker)}/models/${encodeURIComponent(name)}/stop`, {
    method: 'DELETE',
    timeout: WRITE_TIMEOUT,
  })
}

/** POST /api/v1/models/{name}/start */
export async function startModel(m: Machine, name: string): Promise<APIResponse> {
  return request(m, `/api/v1/models/${encodeURIComponent(name)}/start`, {
    method: 'POST',
    timeout: WRITE_TIMEOUT,
  })
}

/** DELETE /api/v1/models/{name}/stop */
export async function stopModel(m: Machine, name: string): Promise<APIResponse> {
  return request(m, `/api/v1/models/${encodeURIComponent(name)}/stop`, {
    method: 'DELETE',
    timeout: WRITE_TIMEOUT,
  })
}

/**
 * GET /api/v1/config — returns the raw config file text so it can be edited
 * verbatim. Unlike the other endpoints this one returns the file body, not an
 * APIResponse envelope.
 */
export async function getConfigText(m: Machine): Promise<string> {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), READ_TIMEOUT)
  try {
    const res = await fetch(url(m, '/api/v1/config'), {
      headers: { 'api-key': m.apiKey },
      signal: ctrl.signal,
    })
    if (!res.ok) {
      let msg = `HTTP ${res.status}`
      try {
        msg = (await res.json())?.message ?? msg
      } catch {
        // keep HTTP status message
      }
      throw new ApiError(msg, res.status)
    }
    return await res.text()
  } catch (e) {
    throw wrapError(e)
  } finally {
    clearTimeout(timer)
  }
}

/** PUT /api/v1/config — body is the raw config JSON. */
export async function saveConfigText(m: Machine, text: string): Promise<APIResponse> {
  return request(m, '/api/v1/config', {
    method: 'PUT',
    timeout: WRITE_TIMEOUT,
    headers: { 'Content-Type': 'application/json' },
    body: text,
  })
}
