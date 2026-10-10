<script lang="ts">
  import { listModels, startModel, stopModel } from '../lib/api'
  import { machines, refreshMachine, statuses } from '../lib/store'
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

<a class="back" href="#/fleet">← Fleet</a>

{#if !machine}
  <p>Machine not found. <a href="#/fleet">Back to fleet</a>.</p>
{:else}
  <h1>
    <span class="dot {status?.state ?? 'checking'}"></span>
    {machine.name}
    {#if runningModel}
      <span class="muted">· {runningModel}</span>
    {/if}
  </h1>

  <div class="row toolbar">
    <button class="btn" onclick={load} disabled={loading}>⟳ Refresh</button>
    <a class="btn" href={`#/machine/${id}/config`}>Edit config</a>
  </div>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if loadError}
    <p class="error">{loadError}</p>
  {:else if models.length === 0}
    <p class="muted">No models configured.</p>
  {:else}
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
              {item.name}
              {#if item.active}<span class="badge running">running</span>{/if}
            </td>
            <td class="path" title={item.launch_cmd || item.model_path}>
              {item.launch_cmd || item.model_path}
            </td>
            <td>{item.port ?? ''}</td>
            <td class="actions">
              {#if item.active}
                {#if confirmStop}
                  <span class="confirm-inline">
                    Stop {item.name}?
                    <button class="btn danger" disabled={busyModel !== null} onclick={() => doStop(item.name)}>
                      Yes
                    </button>
                    <button class="btn" onclick={() => (confirmStop = false)}>No</button>
                  </span>
                {:else}
                  <button class="btn danger" disabled={busyModel !== null} onclick={() => (confirmStop = true)}>
                    ■ Stop
                  </button>
                {/if}
              {:else}
                <button
                  class="btn primary"
                  disabled={busyModel !== null || !!runningModel}
                  title={runningModel ? `Stop ${runningModel} first — the manager runs one model at a time` : ''}
                  onclick={() => doStart(item.name)}
                >
                  {busyModel === item.name ? 'Starting…' : '▶ Start'}
                </button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
{/if}

<style>
  .back {
    font-size: 0.85rem;
  }

  h1 {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .toolbar {
    margin: 0.4rem 0 1rem;
  }

  .confirm-inline {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
  }
</style>
