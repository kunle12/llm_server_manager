<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from './lib/Icon.svelte'
  import { hash } from './lib/router'
  import {
    bootstrapLocalMachine,
    fleetNodeCount,
    fleetOfflineCount,
    refreshAll,
    setPollingActive,
    settings,
  } from './lib/store'
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
  const routeKey = $derived(`${view}/${parts[1] ?? ''}/${parts[2] ?? ''}`)

  // The screen is an internal scroll container, so routes do not reset the
  // browser scroll position for us. Start each view at its top instead of
  // inheriting the previous page's scroll offset.
  let screenEl = $state<HTMLElement>()
  $effect(() => {
    void routeKey
    screenEl?.scrollTo(0, 0)
  })

  const contextLabel = $derived(
    view === 'machine' && parts[1]
      ? parts[2] === 'config'
        ? 'Configuration'
        : 'Machine control'
      : 'Fleet control',
  )

  // A coordinator may report offline workers while its own status is fine.
  const offlineCount = $derived($fleetOfflineCount)
  const nominal = $derived(offlineCount === 0)

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

  // Stardates are a convention, not a real clock: anchor the TNG rate
  // (1000 stardates per year) at a fixed instant and let it advance.
  let stardate = $state('—')
  function tickStardate() {
    const SD_EPOCH = Date.UTC(2026, 0, 1)
    const SD_BASE = 79000
    const YEAR_MS = 31556952000
    stardate = (SD_BASE + ((Date.now() - SD_EPOCH) / YEAR_MS) * 1000).toFixed(1)
  }
  onMount(() => {
    tickStardate()
    const id = setInterval(tickStardate, 30000)
    return () => clearInterval(id)
  })
</script>

<div class="console">
  <header class="topbar">
    <a class="brand" href="#/fleet" aria-label="LLM Fleet Manager">
      <span class="brand-mark"><Icon name="bolt" size={15} /></span>
      <span>LLM FLEET <b>·</b> MANAGER</span>
    </a>
    <div class="seg seg-context" title="Current section">{contextLabel}</div>

    <span class="spacer"></span>

    <div class="topbar-controls">
      {#if onFleet}
        <label class="poll-label">
          <span>Refresh</span>
          <select value={$settings.pollMs} onchange={setPollMs} title="Poll interval">
            {#each pollOptions as ms (ms)}
              <option value={ms}>{ms / 1000}s</option>
            {/each}
          </select>
        </label>
      {/if}
      <button
        class="icon-btn"
        class:is-paused={$settings.paused}
        onclick={togglePause}
        title={$settings.paused ? 'Resume auto-refresh' : 'Pause auto-refresh'}
        aria-label={$settings.paused ? 'Resume auto-refresh' : 'Pause auto-refresh'}
      >
        <Icon name={$settings.paused ? 'play' : 'pause'} />
      </button>
      {#if onFleet}
        <button
          class="icon-btn accent-sky"
          onclick={() => refreshAll()}
          title="Refresh now"
          aria-label="Refresh now"
        >
          <Icon name="refresh" />
        </button>
      {/if}
      <button
        class="icon-btn accent-lilac"
        onclick={toggleTheme}
        title="Toggle light/dark"
        aria-label="Toggle light/dark"
      >
        <Icon name={$settings.theme === 'dark' ? 'sun' : 'moon'} />
      </button>
      <button
        class="icon-btn accent-peach"
        onclick={() => (showSettings = true)}
        title="Machines & settings"
        aria-label="Machines and settings"
      >
        <Icon name="gear" />
      </button>
    </div>

    <div class="readout">
      <span class="led" class:warn={!nominal}></span>
      <span class="sys">{nominal ? 'all systems nominal' : `${offlineCount} offline`}</span>
      <span class="sep">·</span>
      <b>{$fleetNodeCount} {$fleetNodeCount === 1 ? 'node' : 'nodes'}</b>
    </div>
  </header>

  <div class="screen" bind:this={screenEl}>
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
  </div>

  <footer class="bottombar" aria-hidden="true">
    <div class="seg b1">
      {nominal ? 'fleet control nominal' : `${offlineCount} signal${offlineCount === 1 ? '' : 's'} lost`}
    </div>
    <div class="seg b2">
      {$settings.paused ? 'refresh paused' : `refresh ${$settings.pollMs / 1000}s`}
    </div>
    <div class="seg b3">stardate {stardate}</div>
    <div class="seg b4">α</div>
  </footer>
</div>

{#if showSettings}
  <SettingsDialog onclose={() => (showSettings = false)} />
{/if}
<Toasts />
