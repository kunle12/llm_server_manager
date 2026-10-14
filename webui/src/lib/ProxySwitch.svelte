<script lang="ts">
  import { changeProxy } from '../lib/proxy'
  import { refreshProxy, proxies } from '../lib/store'
  import type { Machine } from '../lib/types'

  let { machine, compact = false }: { machine: Machine; compact?: boolean } = $props()

  let busy = $state(false)
  const enabled = $derived($proxies[machine.id])

  async function toggle(e: Event) {
    const next = (e.currentTarget as HTMLInputElement).checked
    busy = true
    try {
      await changeProxy(machine, next)
    } finally {
      busy = false
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
    {#if !compact}
      <span class="switch-text">{busy ? 'Saving…' : enabled ? 'Proxy on' : 'Proxy off'}</span>
    {/if}
  </label>
{/if}
