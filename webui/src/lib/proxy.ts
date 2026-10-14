import { ApiError, fetchConfig, saveConfig } from './api'
import { setProxyInConfigText } from './config-edit'
import { refreshProxy } from './store'
import { toast } from './toast'
import type { Machine } from './types'

/**
 * Toggle the manager's OpenAI-compatible proxy. This edits the "proxy" key in
 * the machine's config file (format-preserving) using the same ETag/If-Match
 * flow as the config editor, so a concurrent edit is detected rather than
 * clobbered. The server applies the change on reload, without a restart.
 */
export async function changeProxy(machine: Machine, enabled: boolean): Promise<void> {
  try {
    const snap = await fetchConfig(machine)
    const next = setProxyInConfigText(snap.text, enabled)
    await saveConfig(machine, next, snap.etag)
    toast('ok', enabled ? 'Proxy enabled' : 'Proxy disabled')
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      toast('err', 'Config changed elsewhere — reload it and try again')
    } else {
      toast('err', `Proxy update failed: ${e instanceof Error ? e.message : e}`)
    }
  } finally {
    // Re-read the real state so the switch never shows a stale value.
    await refreshProxy(machine)
  }
}
