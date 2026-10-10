<script lang="ts">
  import { hash } from './lib/router'
  import { refreshAll, settings } from './lib/store'
  import Toasts from './lib/Toasts.svelte'
  import ConfigEditor from './views/ConfigEditor.svelte'
  import Fleet from './views/Fleet.svelte'
  import Machine from './views/Machine.svelte'
  import SettingsDialog from './views/SettingsDialog.svelte'

  let showSettings = $state(false)

  const parts = $derived($hash.replace(/^#\/?/, '').split('/').filter(Boolean))
  const view = $derived(parts[0] ?? 'fleet')

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
</script>

<header>
  <a class="brand" href="#/fleet">⚡ LLM Fleet</a>
  <div class="spacer"></div>
  {#if view === 'fleet'}
    <select value={$settings.pollMs} onchange={setPollMs} title="Poll interval">
      {#each pollOptions as ms (ms)}
        <option value={ms}>{ms / 1000}s</option>
      {/each}
    </select>
    <button
      class="btn icon"
      onclick={togglePause}
      title={$settings.paused ? 'Resume polling' : 'Pause polling'}
    >
      {$settings.paused ? '▶' : '⏸'}
    </button>
    <button class="btn icon" onclick={() => refreshAll()} title="Refresh now">⟳</button>
  {/if}
  <button class="btn icon" onclick={toggleTheme} title="Toggle theme">
    {$settings.theme === 'dark' ? '☀' : '🌙'}
  </button>
  <button class="btn icon" onclick={() => (showSettings = true)} title="Machines & settings">
    ⚙
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
