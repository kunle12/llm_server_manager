<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from './lib/Icon.svelte'
  import { hash } from './lib/router'
  import { bootstrapLocalMachine, refreshAll, setPollingActive, settings } from './lib/store'
  import Toasts from './lib/Toasts.svelte'
  import ConfigEditor from './views/ConfigEditor.svelte'
  import Fleet from './views/Fleet.svelte'
  import Machine from './views/Machine.svelte'
  import SettingsDialog from './views/SettingsDialog.svelte'

  let showSettings = $state(false)

  const parts = $derived($hash.replace(/^#\/?/, '').split('/').filter(Boolean))
  const view = $derived(parts[0] ?? 'fleet')
  // The render below falls back to Fleet for any unknown route, so polling
  // must follow the rendered view, not only the literal "#/fleet" hash.
  const onFleet = $derived(!(view === 'machine' && !!parts[1]))

  const pollOptions = [2000, 5000, 10000, 30000]

  function setPollMs(e: Event) {
    const ms = Number((e.target as HTMLSelectElement).value)
    settings.update((s) => ({ ...s, pollMs: ms }))
  }

  function togglePause() {
    settings.update((s) => ({ ...s, paused: !s.paused }))
  }

  function toggleTheme() {
    settings.update((s) => ({ ...s, theme: s.theme === 'dark' ? 'light' : 'dark' }))
  }

  $effect(() => {
    document.documentElement.dataset.theme = $settings.theme
  })

  // Public fleet status is only polled while the Fleet view is on screen.
  let lastOnFleet: boolean | null = null
  $effect(() => {
    const active = onFleet
    setPollingActive(active)
    if (active && lastOnFleet === false) {
      void refreshAll()
    }
    lastOnFleet = active
  })

  // Attach the manager that served this UI on first run, then load its fleet.
  onMount(async () => {
    await bootstrapLocalMachine()
    if (onFleet) await refreshAll()
  })
</script>

<header>
  <a class="brand" href="#/fleet" aria-label="LLM Fleet Manager">
    <span class="brand-mark"><Icon name="bolt" size={15} /></span>
    <span>LLM Fleet Manager</span>
  </a>
  <div class="spacer"></div>

  {#if onFleet}
    <label class="poll-label">
      <span>Auto-refresh</span>
      <select value={$settings.pollMs} onchange={setPollMs} title="Poll interval">
        {#each pollOptions as ms (ms)}
          <option value={ms}>{ms / 1000}s</option>
        {/each}
      </select>
    </label>
    <button
      class="icon-btn"
      onclick={togglePause}
      title={$settings.paused ? 'Resume auto-refresh' : 'Pause auto-refresh'}
      aria-label={$settings.paused ? 'Resume auto-refresh' : 'Pause auto-refresh'}
    >
      <Icon name={$settings.paused ? 'play' : 'pause'} />
    </button>
    <button class="icon-btn" onclick={() => refreshAll()} title="Refresh now" aria-label="Refresh now">
      <Icon name="refresh" />
    </button>
  {/if}

  <button
    class="icon-btn"
    onclick={toggleTheme}
    title="Toggle light/dark"
    aria-label="Toggle light/dark"
  >
    <Icon name={$settings.theme === 'dark' ? 'sun' : 'moon'} />
  </button>
  <button
    class="icon-btn"
    onclick={() => (showSettings = true)}
    title="Machines & settings"
    aria-label="Machines and settings"
  >
    <Icon name="gear" />
  </button>
</header>

<main>
  {#if view === 'machine' && parts[1]}
    {#if parts[2] === 'config'}
      <ConfigEditor id={parts[1]} />
    {:else}
      <Machine id={parts[1]} />
    {/if}
  {:else}
    <Fleet onopenSettings={() => (showSettings = true)} />
  {/if}
</main>

{#if showSettings}
  <SettingsDialog onclose={() => (showSettings = false)} />
{/if}
<Toasts />
