<script lang="ts">
  import Icon from '../lib/Icon.svelte'
  import ProxySwitch from '../lib/ProxySwitch.svelte'
  import { listModels, startModel, stopModel } from '../lib/api'
  import { machines, refreshMachine, refreshProxy, statuses } from '../lib/store'
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

  const runningModel = $derived(status?.state === 'running' ? status.model : null)

  async function load() {
    if (!machine) return
    loading = true
    loadError = ''
    try {
      const list = await listModels(machine)
      models = list
      await refreshMachine(machine)
      await refreshProxy(machine)
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
      await refreshMachine(machine)
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
      await refreshMachine(machine)
    }
  }
</script>

<a class="back" href="#/fleet"><Icon name="chevronLeft" size={15} /> Fleet</a>

{#if !machine}
  <p>Machine not found. <a href="#/fleet">Back to fleet</a>.</p>
{:else}
  <div class="page-head">
    <div>
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
      <a class="btn" href={`#/machine/${id}/config`}>
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
          {#each models as item (item.name)}
            <tr class:active={item.active}>
              <td>
                <span class="model-name">{item.alias || item.name}</span>
                {#if item.alias}<span class="alias" title="Proxy alias">→ {item.name}</span>{/if}
                {#if item.active}<span class="pill running status-inline">Running</span>{/if}
              </td>
              <td class="path" title={item.launch_cmd || item.model_path}>
                {item.launch_cmd || item.model_path}
              </td>
              <td>{item.port ?? ''}</td>
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
  }

  .model-name {
    font-weight: 550;
  }

  .alias {
    color: var(--muted);
    font-size: 0.82rem;
    margin-left: 0.4rem;
  }

  .status-inline {
    margin-left: 0.5rem;
  }
</style>
