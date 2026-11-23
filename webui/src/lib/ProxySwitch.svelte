<script lang="ts">
  import { changeProxy } from '../lib/proxy'
  import { refreshProxy, proxies } from '../lib/store'
  import type { Machine } from '../lib/types'

  let { machine, compact = false }: { machine: Machine; compact?: boolean } = $props()

  let busy = $state(false)
  const enabled = $derived($proxies[machine.id])

  async function toggle(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    const next = input.checked
    busy = true
    try {
      await changeProxy(machine, next)
    } finally {
      busy = false
      // The checkbox flips itself before the change is confirmed. If the save
      // failed, changeProxy's refresh leaves the store at the old value and
      // Svelte may not re-write the DOM because the value did not change, so
      // snap the control back to the authoritative state explicitly.
      input.checked = $proxies[machine.id] ?? false
    }
  }

  // Make sure we have a state to render even if refreshAll hasn't run yet.
  let probedId = ''
  $effect(() => {
    if (machine && machine.id !== probedId) {
      probedId = machine.id
      if ($proxies[machine.id] === undefined) void refreshProxy(machine)
    }
  })
</script>

{#if enabled !== undefined}
  <label
    class="switch"
    class:compact
    title={`Proxy ${enabled ? 'on' : 'off'} — click to toggle the OpenAI-compatible /v1 proxy`}
  >
    <input
      type="checkbox"
      checked={enabled}
      disabled={busy}
      onchange={toggle}
      aria-label="Toggle the OpenAI-compatible proxy"
    />
    <span class="track"></span>
    <span class="switch-text">{busy ? 'Saving…' : enabled ? 'Proxy on' : 'Proxy off'}</span>
  </label>
{/if}
