<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { machines, newMachineId } from '../lib/store'
  import { toast } from '../lib/toast'
  import type { Machine } from '../lib/types'

  let { onclose }: { onclose: () => void } = $props()

  let draft = $state<Machine[]>($machines.map((m) => ({ ...m })))
  let importText = $state('')
  let showKeys = $state(false)
  let modalEl = $state<HTMLElement>()

  function normalize(m: Machine): Machine {
    return {
      id: m.id || newMachineId(),
      name: m.name.trim() || 'machine',
      url: m.url.trim().replace(/\/+$/, ''),
      apiKey: m.apiKey.trim(),
    }
  }

  function add() {
    draft.push({
      id: newMachineId(),
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

  // Escape closes the dialog, like every other panel on the console.
  onMount(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    // Focus the panel itself (not an input: that would pop the keyboard on
    // phones). This announces the dialog and moves focus out of the page.
    modalEl?.focus()

    const focusables = () =>
      Array.from(
        modalEl?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ) ?? [],
      )

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onclose()
        return
      }
      if (e.key !== 'Tab') return

      const items = focusables()
      if (items.length === 0) {
        e.preventDefault()
        modalEl?.focus()
        return
      }
      const first = items[0]
      const last = items[items.length - 1]
      const active = document.activeElement

      if (!modalEl?.contains(active)) {
        e.preventDefault()
        const target = e.shiftKey ? last : first
        target.focus()
      } else if (e.shiftKey && (active === first || active === modalEl)) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && active === last) {
        e.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      previous?.focus()
    }
  })
</script>

<div class="overlay" onclick={onOverlayClick} role="presentation">
  <div
    class="modal card"
    role="dialog"
    aria-modal="true"
    aria-labelledby="settings-title"
    tabindex="-1"
    bind:this={modalEl}
  >
    <div class="modal-cap">
      <span class="cap-title">access panel · fleet registry</span>
      <button class="cap-close" onclick={onclose} title="Close" aria-label="Close settings">
        <Icon name="x" size={15} />
      </button>
    </div>

    <div class="modal-body">
      <section>
        <span class="kicker">crew access</span>
        <h2 id="settings-title">Machines</h2>
        <p class="muted hint">Each entry is one llm_server_manager instance the UI can control.</p>

        {#if draft.length === 0}
          <p class="muted">No machines configured.</p>
        {/if}

        {#each draft as m, i (i)}
          <div class="machine-fields">
            <label class="machine-field">
              <span class="field-label">name</span>
              <input bind:value={m.name} placeholder="gpu-01" aria-label="Machine name" />
            </label>
            <label class="machine-field">
              <span class="field-label">manager url</span>
              <input bind:value={m.url} placeholder="http://host:8080" aria-label="Manager URL" />
            </label>
            <label class="machine-field">
              <span class="field-label">api key</span>
              <input
                bind:value={m.apiKey}
                placeholder="optional"
                aria-label="API key"
                type={showKeys ? 'text' : 'password'}
              />
            </label>
            <button
              class="icon-btn danger"
              onclick={() => remove(i)}
              title="Remove machine"
              aria-label="Remove machine"
            >
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
        <span class="kicker">data interchange</span>
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
    </div>

    <div class="modal-foot">
      <span class="foot-note">local registry · stored in this browser</span>
      <button class="btn subtle" onclick={onclose}>Cancel</button>
      <button class="btn primary" onclick={save}>Save</button>
    </div>

    <div class="modal-ribbon" aria-hidden="true">
      <span class="r1">local registry</span>
      <span class="r2">json</span>
      <span class="r3">secure</span>
      <span class="r4">α</span>
    </div>
  </div>
</div>

<style>
  .hint {
    margin: 0 0 1rem;
    font-size: 0.85rem;
  }

  .modal-body h2 {
    margin-bottom: 0.25rem;
  }

  .settings-row {
    margin-top: 0.7rem;
    flex-wrap: wrap;
  }

  .check {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    margin-left: 0.4rem;
    font-family: var(--mono);
    font-size: 8px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
  }

  .check input {
    width: 16px;
    height: 16px;
    accent-color: var(--amber);
  }

  textarea.code {
    min-height: 8rem;
  }
</style>
