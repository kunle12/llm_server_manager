<script lang="ts">
  import { machines } from '../lib/store'
  import { toast } from '../lib/toast'
  import type { Machine } from '../lib/types'

  let { onclose }: { onclose: () => void } = $props()

  let draft = $state<Machine[]>(structuredClone($machines))
  let importText = $state('')
  let showKeys = $state(false)

  function normalize(m: Machine): Machine {
    return {
      id: m.id || crypto.randomUUID(),
      name: m.name.trim() || 'machine',
      url: m.url.trim().replace(/\/+$/, ''),
      apiKey: m.apiKey.trim(),
    }
  }

  function add() {
    draft.push({
      id: crypto.randomUUID(),
      name: `machine-${draft.length + 1}`,
      url: 'http://localhost:8080',
      apiKey: '',
    })
  }

  function remove(i: number) {
    draft.splice(i, 1)
  }

  function save() {
    machines.set(draft.map(normalize))
    onclose()
  }

  function doExport() {
    importText = JSON.stringify(draft.map(normalize), null, 2)
  }

  function doImport() {
    try {
      const parsed: unknown = JSON.parse(importText)
      if (!Array.isArray(parsed)) throw new Error('expected a JSON array')
      draft = parsed.map((m: Machine) => normalize(m))
      importText = ''
      toast('ok', `Imported ${draft.length} machine(s) — press Save to keep them`)
    } catch (e) {
      toast('err', `Import failed: ${e instanceof Error ? e.message : e}`)
    }
  }

  function onOverlayClick(e: MouseEvent) {
    if (e.target === e.currentTarget) onclose()
  }
</script>

<div class="overlay" onclick={onOverlayClick} role="presentation">
  <div class="modal card" role="dialog" aria-modal="true" aria-label="Machines">
    <h2>Machines</h2>

    {#if draft.length === 0}
      <p class="muted">No machines configured.</p>
    {/if}

    {#each draft as m, i (i)}
      <div class="machine-row">
        <input bind:value={m.name} placeholder="name" aria-label="Machine name" />
        <input bind:value={m.url} placeholder="http://host:8080" aria-label="Manager URL" />
        <input
          bind:value={m.apiKey}
          placeholder="api key (optional)"
          aria-label="API key"
          type={showKeys ? 'text' : 'password'}
        />
        <button class="btn danger" onclick={() => remove(i)} title="Remove">✕</button>
      </div>
    {/each}

    <div class="row">
      <button class="btn" onclick={add}>+ Add machine</button>
      <label class="muted">
        <input type="checkbox" bind:checked={showKeys} /> show keys
      </label>
    </div>

    <h2>Import / export</h2>
    <textarea
      bind:value={importText}
      rows="6"
      class="code"
      placeholder="Paste fleet JSON here, or press Export and copy"
    ></textarea>
    <div class="row" style="margin-top: 0.5rem">
      <button class="btn" onclick={doExport}>Export</button>
      <button class="btn" onclick={doImport}>Import</button>
    </div>

    <div class="row end">
      <button class="btn" onclick={onclose}>Cancel</button>
      <button class="btn primary" onclick={save}>Save</button>
    </div>
  </div>
</div>

<style>
  h2 {
    margin-top: 0.4rem;
  }

  .machine-row {
    display: grid;
    grid-template-columns: 1fr 2fr 1.2fr auto;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }

  .code {
    width: 100%;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.8rem;
    white-space: pre;
  }
</style>
