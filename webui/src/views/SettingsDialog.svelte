<script lang="ts">
  import Icon from '../lib/Icon.svelte'
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
    <section>
      <h2>Machines</h2>
      <p class="muted hint">Each entry is one llm_server_manager instance the UI can control.</p>

      {#if draft.length === 0}
        <p class="muted">No machines configured.</p>
      {/if}

      {#each draft as m, i (i)}
        <div class="machine-row">
          <input bind:value={m.name} placeholder="Name" aria-label="Machine name" />
          <input bind:value={m.url} placeholder="http://host:8080" aria-label="Manager URL" />
          <input
            bind:value={m.apiKey}
            placeholder="API key (optional)"
            aria-label="API key"
            type={showKeys ? 'text' : 'password'}
          />
          <button class="icon-btn danger" onclick={() => remove(i)} title="Remove machine" aria-label="Remove machine">
            <Icon name="trash" size={16} />
          </button>
        </div>
      {/each}

      <div class="row settings-row">
        <button class="btn subtle" onclick={add}><Icon name="plus" size={15} /> Add machine</button>
        <label class="check muted">
          <input type="checkbox" bind:checked={showKeys} /> Show keys
        </label>
      </div>
    </section>

    <section>
      <h2>Import / export</h2>
      <textarea
        bind:value={importText}
        rows="6"
        class="code"
        spellcheck="false"
        placeholder="Paste fleet JSON here, or press Export and copy"
      ></textarea>
      <div class="row settings-row">
        <button class="btn subtle" onclick={doExport}><Icon name="external" size={15} /> Export</button>
        <button class="btn subtle" onclick={doImport}><Icon name="box" size={15} /> Import</button>
      </div>
    </section>

    <div class="modal-foot">
      <button class="btn subtle" onclick={onclose}>Cancel</button>
      <button class="btn primary" onclick={save}>Save</button>
    </div>
  </div>
</div>

<style>
  .hint {
    margin: 0 0 0.9rem;
    font-size: 0.88rem;
  }

  .settings-row {
    margin-top: 0.7rem;
  }

  .check {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.85rem;
    margin-left: 0.4rem;
  }

  .check input {
    width: 16px;
    height: 16px;
    accent-color: var(--accent);
  }

  textarea.code {
    min-height: 8rem;
  }
</style>
