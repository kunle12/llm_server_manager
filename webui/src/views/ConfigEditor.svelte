<script lang="ts">
  import Icon from '../lib/Icon.svelte'
  import { highlightJSON } from '../lib/highlight'
  import { ApiError, fetchConfig, saveConfig, type ConfigSnapshot } from '../lib/api'
  import { machines, refreshAll, settings } from '../lib/store'
  import { toast } from '../lib/toast'

  let { id }: { id: string } = $props()

  const machine = $derived($machines.find((m) => m.id === id) ?? null)

  let text = $state('')
  let savedText = $state('')
  let diskEtag = $state('')
  let loading = $state(true)
  let loadError = $state('')
  let saving = $state(false)

  // A newer version of the file seen on disk (external edit / another machine).
  let external = $state<{ text: string; etag: string } | null>(null)
  let dismissedEtag = $state('')
  // The last save was rejected because the file changed underneath us.
  let conflict = $state(false)

  let preEl = $state<HTMLElement>()
  let areaEl = $state<HTMLTextAreaElement>()

  const dirty = $derived(text !== savedText)
  const validationError = $derived(validateConfig(text))
  const highlighted = $derived(highlightJSON(text) + '\n')
  const showExternal = $derived(external !== null && external.etag !== dismissedEtag && !conflict)

  /** Client-side pre-flight checks mirroring the server's validation. */
  function validateConfig(t: string): string {
    if (!t.trim()) return 'Config is empty'
    let parsed: unknown
    try {
      parsed = JSON.parse(t)
    } catch (e) {
      return `Invalid JSON: ${e instanceof Error ? e.message : e}`
    }
    const raw = parsed as { models?: unknown }
    const models = raw?.models
    // An empty (or absent) model list is allowed: models can be added later.
    if (models !== undefined && !Array.isArray(models)) {
      return '"models" must be an array'
    }
    const list = Array.isArray(models) ? models : []
    const seen = new Set<string>()
    for (let i = 0; i < list.length; i++) {
      const m = list[i] as { name?: unknown }
      if (typeof m?.name !== 'string' || !m.name) {
        return `Model at index ${i} is missing a name`
      }
      if (seen.has(m.name)) return `Duplicate model name "${m.name}"`
      seen.add(m.name)
    }
    return ''
  }

  function applySnapshot(snap: ConfigSnapshot) {
    text = snap.text
    savedText = snap.text
    diskEtag = snap.etag
    external = null
    dismissedEtag = ''
    conflict = false
  }

  async function load() {
    if (!machine) return
    loading = true
    loadError = ''
    try {
      applySnapshot(await fetchConfig(machine))
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  // Reload whenever the machine (id) changes.
  $effect(() => {
    void machine
    load()
  })

  // Poll for external edits. A clean editor follows the file silently; a dirty
  // one shows a banner so the user can decide before overwriting.
  let checking = false
  async function checkRemote() {
    if (!machine || saving || checking || loading) return
    checking = true
    try {
      const snap = await fetchConfig(machine, diskEtag)
      if (snap.notModified) {
        // Disk matches what we loaded — clear a banner left by a change that
        // has since been reverted.
        if (external) external = null
        return
      }
      if (!dirty) {
        applySnapshot(snap)
      } else if (!external || external.etag !== snap.etag) {
        external = { text: snap.text, etag: snap.etag }
      }
    } catch {
      // polling is best-effort (offline, old binary, …)
    } finally {
      checking = false
    }
  }

  $effect(() => {
    if (!machine || $settings.paused) return
    const period = $settings.pollMs
    const timer = setInterval(() => void checkRemote(), period)
    return () => clearInterval(timer)
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

  async function reloadFromDisk() {
    if (!machine) return
    try {
      applySnapshot(await fetchConfig(machine))
      toast('ok', 'Reloaded configuration from disk')
    } catch (e) {
      toast('err', `Reload failed: ${e instanceof Error ? e.message : e}`)
    }
  }

  async function save(force = false) {
    if (!machine || validationError || (!dirty && !force)) return
    saving = true
    try {
      const res = await saveConfig(machine, text, force ? '*' : diskEtag)
      savedText = text
      diskEtag = res.etag
      external = null
      dismissedEtag = ''
      conflict = false
      toast('ok', res.message || 'Config saved')
      await refreshAll()
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        conflict = true
        // Pull the current disk version so "Reload" can show it.
        try {
          const snap = await fetchConfig(machine)
          external = { text: snap.text, etag: snap.etag }
        } catch {
          // ignore
        }
        toast('err', 'The file changed on disk — resolve the conflict to continue')
      } else {
        toast('err', `Save failed: ${e instanceof Error ? e.message : e}`)
      }
    } finally {
      saving = false
    }
  }

  /** Keep the highlighted layer aligned with the textarea while scrolling. */
  function syncScroll() {
    if (!preEl || !areaEl) return
    preEl.scrollTop = areaEl.scrollTop
    preEl.scrollLeft = areaEl.scrollLeft
  }

  /** Tab inserts two spaces instead of moving focus. */
  function onKeydown(e: KeyboardEvent) {
    if (e.key !== 'Tab' || !areaEl) return
    e.preventDefault()
    const start = areaEl.selectionStart
    const end = areaEl.selectionEnd
    text = text.slice(0, start) + '  ' + text.slice(end)
    requestAnimationFrame(() => {
      areaEl!.selectionStart = areaEl!.selectionEnd = start + 2
    })
  }
</script>

<a class="back" href={`#/machine/${id}`}>
  <Icon name="chevronLeft" size={15} /> {machine?.name ?? 'Machine'}
</a>

{#if !machine}
  <p>Machine not found. <a href="#/fleet">Back to fleet</a>.</p>
{:else}
  <div class="page-head">
    <div>
      <h1>Configuration</h1>
      <p class="sub">Raw JSON for <strong>{machine.name}</strong></p>
    </div>
  </div>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if loadError}
    <div class="card empty">
      <div class="empty-icon"><Icon name="alert" size={26} /></div>
      <h2>Can't load configuration</h2>
      <p class="muted">{loadError}</p>
      <p class="muted">This view needs <code>GET /api/v1/config</code>; older manager builds do not support it.</p>
    </div>
  {:else}
    {#if conflict}
      <div class="banner conflict" role="alert">
        <span class="banner-icon"><Icon name="alert" size={17} /></span>
        <span>This configuration changed on disk after you loaded it, so your save was blocked.</span>
        <span class="spacer"></span>
        <button class="btn subtle" onclick={reloadFromDisk} disabled={saving}>Reload (discard mine)</button>
        <button class="btn danger" onclick={() => save(true)} disabled={saving || !!validationError}>
          Overwrite anyway
        </button>
      </div>
    {:else if showExternal}
      <div class="banner" role="alert">
        <span class="banner-icon"><Icon name="alert" size={17} /></span>
        <span>This configuration was changed on disk (by another editor or machine).</span>
        <span class="spacer"></span>
        <button class="btn subtle" onclick={() => (dismissedEtag = external!.etag)}>Keep editing</button>
        <button class="btn primary" onclick={reloadFromDisk}>Reload from disk</button>
      </div>
    {/if}

    <div class="card">
      <div class="editor-meta">
        {#if validationError}
          <span class="chip err"><Icon name="alert" size={13} /> {validationError}</span>
        {:else}
          <span class="chip ok"><Icon name="check" size={13} /> Valid JSON</span>
        {/if}
        {#if dirty}
          <span class="chip">Unsaved changes</span>
        {/if}
        <div class="spacer"></div>
        <button class="btn subtle" onclick={reloadFromDisk} disabled={saving} title="Reload from disk">
          <Icon name="refresh" size={15} /> Revert
        </button>
        <button
          class="btn primary"
          onclick={() => save()}
          disabled={saving || !!validationError || !dirty || conflict}
        >
          <Icon name="check" size={15} />
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
      <div class="editor-wrap">
        <div class="code-editor">
          <pre class="code-highlight" aria-hidden="true" bind:this={preEl}><code>{@html highlighted}</code></pre>
          <textarea
            class="code-input"
            bind:this={areaEl}
            bind:value={text}
            onscroll={syncScroll}
            onkeydown={onKeydown}
            spellcheck="false"
            autocapitalize="off"
            autocomplete="off"
            wrap="off"
            aria-label="Configuration JSON"
          ></textarea>
        </div>
      </div>
    </div>

    <p class="editor-help muted">
      Saving writes the manager's config file, which reloads automatically. A running model is never
      restarted by a save. If the file was edited elsewhere, you'll be asked before overwriting.
    </p>
  {/if}
{/if}

<style>
  .editor-help {
    margin: 0.75rem 0.25rem 0;
    font-size: 0.85rem;
  }

  .editor-meta .btn {
    flex: none;
  }
</style>
