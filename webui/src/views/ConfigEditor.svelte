<script lang="ts">
  import { getConfigText, saveConfigText } from '../lib/api'
  import { machines, refreshAll } from '../lib/store'
  import { toast } from '../lib/toast'

  let { id }: { id: string } = $props()

  const machine = $derived($machines.find((m) => m.id === id) ?? null)

  let text = $state('')
  let savedText = $state('')
  let loading = $state(true)
  let loadError = $state('')
  let saving = $state(false)

  const dirty = $derived(text !== savedText)
  const validationError = $derived(validateConfig(text))

  /** Client-side pre-flight checks mirroring the server's validation. */
  function validateConfig(t: string): string {
    if (!t.trim()) return 'Config is empty'
    let parsed: unknown
    try {
      parsed = JSON.parse(t)
    } catch (e) {
      return `Invalid JSON: ${e instanceof Error ? e.message : e}`
    }
    const models = (parsed as { models?: unknown })?.models
    if (!Array.isArray(models) || models.length === 0) {
      return '"models" must be a non-empty array'
    }
    const seen = new Set<string>()
    for (let i = 0; i < models.length; i++) {
      const m = models[i] as { name?: unknown }
      if (typeof m?.name !== 'string' || !m.name) {
        return `Model at index ${i} is missing a name`
      }
      if (seen.has(m.name)) return `Duplicate model name "${m.name}"`
      seen.add(m.name)
    }
    return ''
  }

  async function load() {
    if (!machine) return
    loading = true
    loadError = ''
    try {
      const raw = await getConfigText(machine)
      text = raw
      savedText = raw
    } catch (e) {
      loadError =
        e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  // Reload whenever the machine (id) changes.
  $effect(() => {
    void machine
    load()
  })

  // Warn before closing the tab with unsaved changes.
  $effect(() => {
    if (!dirty) return
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault()
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  })

  async function save() {
    if (!machine || validationError || !dirty) return
    saving = true
    try {
      const res = await saveConfigText(machine, text)
      savedText = text
      toast('ok', res.message || 'Config saved')
      await refreshAll()
    } catch (e) {
      toast('err', `Save failed: ${e instanceof Error ? e.message : e}`)
    } finally {
      saving = false
    }
  }
</script>

<a class="back" href={`#/machine/${id}`}>← {machine?.name ?? 'Machine'}</a>

{#if !machine}
  <p>Machine not found. <a href="#/fleet">Back to fleet</a>.</p>
{:else}
  <h1>Config <span class="muted">· raw JSON</span></h1>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if loadError}
    <p class="error">{loadError}</p>
    <p class="muted">
      This view needs <code>GET /api/v1/config</code>; older manager builds do not support it.
    </p>
  {:else}
    <div class="editor-meta">
      {#if validationError}
        <span class="error">✗ {validationError}</span>
      {:else}
        <span class="ok">✓ Valid</span>
      {/if}
      {#if dirty}
        <span class="chip">unsaved changes</span>
      {/if}
      <div class="spacer"></div>
      <button class="btn" onclick={load} disabled={saving}>↺ Revert</button>
      <button class="btn primary" onclick={save} disabled={saving || !!validationError || !dirty}>
        {saving ? 'Saving…' : '💾 Save'}
      </button>
    </div>

    <textarea class="code" bind:value={text} spellcheck="false" rows="22"></textarea>

    <p class="muted">
      Saving writes the manager's config file; it reloads automatically. A running model is
      never restarted by a save.
    </p>
  {/if}
{/if}

<style>
  .back {
    font-size: 0.85rem;
  }

  code {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0 0.3rem;
  }
</style>
