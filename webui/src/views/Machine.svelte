<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ProxySwitch from '../lib/ProxySwitch.svelte'
  import { listModels, startModel, stopModel } from '../lib/api'
  import { machines, refreshMachineStatus, refreshProxy, settings, statuses } from '../lib/store'
  import { toast } from '../lib/toast'
  import type { ModelItem } from '../lib/types'

  let { id }: { id: string } = $props()

  const machine = $derived($machines.find((m) => m.id === id) ?? null)
  const status = $derived($statuses[id])

  let models = $state<ModelItem[]>([])
  let loading = $state(true)
  let loadError = $state('')
  let busyModel = $state<string | null>(null)
  let confirmStop = $state(false)
  let visible = $state(typeof document === 'undefined' ? true : !document.hidden)

  const runningModel = $derived(status?.state === 'running' ? status.model : null)

  const ROW_TINTS = ['var(--sky)', 'var(--peach)', 'var(--lilac)', 'var(--marigold)']

  async function load() {
    if (!machine) return
    loading = true
    loadError = ''
    try {
      const list = await listModels(machine)
      models = list
      await refreshMachineStatus(machine)
      await refreshProxy(machine)
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  /** Refresh the table after start/stop without the full-page loading state. */
  async function reloadModels() {
    if (!machine) return
    try {
      models = await listModels(machine)
    } catch {
      // Keep the current table; the status/error is already surfaced by the header.
    }
  }

  // Prevent a slow status poll from overlapping the next interval tick.
  let pollingStatus = false
  async function pollStatus() {
    if (!machine || pollingStatus) return
    pollingStatus = true
    try {
      await refreshMachineStatus(machine)
      await refreshProxy(machine)
    } finally {
      pollingStatus = false
    }
  }

  // Reload whenever the machine (id) changes.
  $effect(() => {
    void machine
    load()
  })

  // Keep the machine status live while this page is visible. Only this one
  // manager is polled; system telemetry remains Fleet-only.
  $effect(() => {
    void machine
    const period = $settings.pollMs
    if (!machine || $settings.paused || !visible) return
    const timer = setInterval(() => {
      void pollStatus()
    }, period)
    return () => clearInterval(timer)
  })

  onMount(() => {
    const wake = () => {
      visible = !document.hidden
      if (visible && machine) {
        void pollStatus()
      }
    }
    const restored = (event: PageTransitionEvent) => {
      if (event.persisted) wake()
    }
    document.addEventListener('visibilitychange', wake)
    window.addEventListener('pageshow', restored)
    return () => {
      document.removeEventListener('visibilitychange', wake)
      window.removeEventListener('pageshow', restored)
    }
  })

  async function doStart(name: string) {
    if (!machine) return
    busyModel = name
    try {
      const res = await startModel(machine, name)
      toast('ok', res.message || `Starting ${name}`)
    } catch (e) {
      toast('err', `Start failed: ${e instanceof Error ? e.message : e}`)
    } finally {
      busyModel = null
      await refreshMachineStatus(machine)
      await reloadModels()
    }
  }

  async function doStop(name: string) {
    if (!machine) return
    busyModel = name
    try {
      const res = await stopModel(machine, name)
      toast('ok', res.message || `Stopped ${name}`)
    } catch (e) {
      toast('err', `Stop failed: ${e instanceof Error ? e.message : e}`)
    } finally {
      busyModel = null
      confirmStop = false
      await refreshMachineStatus(machine)
      await reloadModels()
    }
  }
</script>

<a class="back" href="#/fleet"><Icon name="chevronLeft" size={15} /> Fleet</a>

{#if !machine}
  <p>Machine not found. <a href="#/fleet">Back to fleet</a>.</p>
{:else}
  <div class="page-head">
    <div class="page-title">
      <span class="kicker">machine control · {status?.state ?? 'checking'}</span>
      <h1>
        <span class="dot {status?.state ?? 'checking'}"></span>
        {machine.name}
      </h1>
      <p class="sub">
        {#if runningModel}
          Running <strong>{runningModel}</strong>
        {:else if status?.state === 'offline'}
          {status.error || 'Offline'}
        {:else}
          No model running
        {/if}
      </p>
    </div>
    <div class="actions">
      <ProxySwitch machine={machine} />
      <button class="btn subtle" onclick={load} disabled={loading}>
        <Icon name="refresh" size={15} /> Refresh
      </button>
      <a class="btn sky" href={`#/machine/${id}/config`}>
        <Icon name="sliders" size={15} /> Edit config
      </a>
    </div>
  </div>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if loadError}
    <p class="error">{loadError}</p>
  {:else if models.length === 0}
    <div class="card empty">
      <div class="empty-icon"><Icon name="box" size={26} /></div>
      <h2>No models configured</h2>
      <p class="muted">Add models to this machine's configuration to start them here.</p>
      <a class="btn" href={`#/machine/${id}/config`}><Icon name="sliders" size={15} /> Edit config</a>
    </div>
  {:else}
    <div class="card list-card">
      <table>
        <thead>
          <tr>
            <th>Model</th>
            <th>Path / command</th>
            <th>Port</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each models as item, i (item.name)}
            {@const mark = ROW_TINTS[i % ROW_TINTS.length]}
            <tr class:active={item.active} style={`--mark: ${mark}`}>
              <td class="model-cell">
                <span class="model-icon"><Icon name={item.active ? 'activity' : 'box'} size={14} /></span>
                <span class="model-text">
                  <span class="model-name">{item.alias || item.name}</span>
                  {#if item.alias}<span class="alias" title="Proxy alias">→ {item.name}</span>{/if}
                  {#if item.active}<span class="pill running status-inline">Running</span>{/if}
                </span>
              </td>
              <td class="path" title={item.launch_cmd || item.model_path}>
                {item.launch_cmd || item.model_path}
              </td>
              <td class="port">{item.port ?? ''}</td>
              <td class="actions">
                {#if item.active}
                  {#if confirmStop}
                    <span class="confirm-inline">
                      Stop {item.alias || item.name}?
                      <button class="btn danger" disabled={busyModel !== null} onclick={() => doStop(item.name)}>
                        Stop
                      </button>
                      <button class="btn subtle" onclick={() => (confirmStop = false)}>Cancel</button>
                    </span>
                  {:else}
                    <button class="btn danger" disabled={busyModel !== null} onclick={() => (confirmStop = true)}>
                      <Icon name="stop" size={15} /> Stop
                    </button>
                  {/if}
                {:else}
                  <button
                    class="btn primary"
                    disabled={busyModel !== null || !!runningModel}
                    title={runningModel
                      ? `Stop ${runningModel} first — the manager runs one model at a time`
                      : ''}
                    onclick={() => doStart(item.name)}
                  >
                    <Icon name="play" size={13} />
                    {busyModel === item.name ? 'Starting…' : 'Start'}
                  </button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
{/if}

<style>
  h1 {
    display: flex;
    align-items: center;
    gap: 0.55rem;
    overflow-wrap: anywhere;
  }

  h1 .dot {
    width: 10px;
    height: 10px;
  }

  .model-cell {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    position: relative;
    padding-left: 1.4rem;
  }

  .model-cell::before {
    content: '';
    position: absolute;
    left: 0.45rem;
    top: 50%;
    transform: translateY(-50%);
    width: 4px;
    height: 24px;
    border-radius: 0 3px 3px 0;
    background: var(--mark, var(--lilac));
  }

  .model-icon {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    flex: none;
    border-radius: 9px;
    background: color-mix(in srgb, var(--mark, var(--lilac)) 55%, transparent);
    color: var(--text);
  }

  .model-text {
    min-width: 0;
  }

  .model-name {
    font-weight: 600;
  }

  .alias {
    color: var(--muted);
    font-family: var(--mono);
    font-size: 0.68rem;
    letter-spacing: 0.04em;
    margin-left: 0.4rem;
  }

  .status-inline {
    margin-left: 0.5rem;
    vertical-align: 1px;
  }

  @media (max-width: 640px) {
    .model-cell {
      padding-left: 1.2rem;
      margin-bottom: 0.15rem;
    }

    .model-cell::before {
      left: 0.25rem;
      height: 26px;
    }

    .alias {
      display: inline-block;
      margin-left: 0.35rem;
    }
  }
</style>
