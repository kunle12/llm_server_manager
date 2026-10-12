<script lang="ts">
  import { machines, fleetViews, refreshMachine, statuses } from '../lib/store'
  import { startModel, stopModel, workerStartModel, workerStopModel } from '../lib/api'
  import { toast } from '../lib/toast'
  import type { FleetMachine, ModelItem } from '../lib/types'

  let { onopenSettings }: { onopenSettings: () => void } = $props()

  let confirmStop = $state<string | null>(null)
  let busy = $state<string | null>(null)
  let pickedModel = $state<Record<string, string>>({})

  function key(machineId: string, entryMachine: string): string {
    return `${machineId}::${entryMachine}`
  }

  function displayName(item: ModelItem): string {
    return item.alias || item.name
  }

  function isLocal(entry: FleetMachine): boolean {
    return entry.machine === 'local'
  }

  async function doStop(machineId: string, entry: FleetMachine, model: string) {
    const m = $machines.find((x) => x.id === machineId)
    if (!m || !model) return
    const k = key(machineId, entry.machine)
    busy = k
    try {
      const res = isLocal(entry)
        ? await stopModel(m, model)
        : await workerStopModel(m, entry.machine, model)
      toast('ok', res.message || `Stopped ${model} on ${entry.machine}`)
    } catch (e) {
      toast('err', `Stop failed: ${e instanceof Error ? e.message : e}`)
    } finally {
      busy = null
      confirmStop = null
      await refreshMachine(m)
    }
  }

  /** Legacy single-card stop (machine without fleet support). */
  async function doStopLocal(machineId: string, model: string) {
    await doStop(machineId, { machine: 'local', status: 'online', models: [] }, model)
  }

  async function doStart(machineId: string, entry: FleetMachine) {
    const m = $machines.find((x) => x.id === machineId)
    if (!m) return
    const k = key(machineId, entry.machine)
    const model = pickedModel[k]
    if (!model) return
    busy = k
    try {
      const res = isLocal(entry)
        ? await startModel(m, model)
        : await workerStartModel(m, entry.machine, model)
      toast('ok', res.message || `Starting ${model} on ${entry.machine}`)
      pickedModel = { ...pickedModel, [k]: '' }
    } catch (e) {
      toast('err', `Start failed: ${e instanceof Error ? e.message : e}`)
    } finally {
      busy = null
      await refreshMachine(m)
    }
  }
</script>

{#if $machines.length === 0}
  <div class="card empty">
    <h2>No machines yet</h2>
    <p class="muted">Add an llm_server_manager instance to see and control its models.</p>
    <button class="btn primary" onclick={onopenSettings}>Add a machine</button>
  </div>
{:else}
  <div class="grid">
    {#each $machines as m (m.id)}
      {@const fv = $fleetViews[m.id]}
      {#if fv === undefined}
        <div class="card machine">
          <div class="row">
            <span class="dot checking"></span>
            <a class="name" href={`#/machine/${m.id}`}>{m.name}</a>
            <span class="badge checking">checking</span>
          </div>
          <div class="muted">checking…</div>
        </div>
      {:else if fv !== null}
        {@const st = $statuses[m.id] ?? { state: 'checking' }}
        <div class="card machine coordinator">
          <div class="row">
            <a class="name" href={`#/machine/${m.id}`}>{m.name}</a>
            <span class="badge fleet">fleet</span>
          </div>
          {#each fv as entry (entry.machine)}
            {@const k = key(m.id, entry.machine)}
            <div class="entry">
              <div class="row">
                <span class="dot {entry.status}"></span>
                <span class="entry-name">{entry.machine}</span>
                {#if isLocal(entry)}<span class="muted small">(this machine)</span>{/if}
              </div>

              {#if entry.status === 'online'}
                {#if entry.running}
                  <div class="model">{entry.running.alias || entry.running.name}</div>
                  <div class="muted">
                    running{entry.running.port ? ` on port ${entry.running.port}` : ''}
                  </div>
                  {#if confirmStop === k}
                    <div class="confirm">
                      <span>Stop {entry.running.alias || entry.running.name}?</span>
                      <button class="btn danger" disabled={busy === k} onclick={() => doStop(m.id, entry, entry.running!.name)}>
                        Yes, stop
                      </button>
                      <button class="btn" onclick={() => (confirmStop = null)}>Cancel</button>
                    </div>
                  {:else}
                    <div class="row end">
                      <button class="btn danger" disabled={busy === k} onclick={() => (confirmStop = k)}>
                        ■ Stop
                      </button>
                    </div>
                  {/if}
                {:else if entry.models.length > 0}
                  <div class="muted">no model running</div>
                  <div class="row startrow">
                    <select
                      class="select"
                      onchange={(e) => (pickedModel = { ...pickedModel, [k]: (e.currentTarget as HTMLSelectElement).value })}
                    >
                      <option value="">start a model…</option>
                      {#each entry.models.filter((x) => !x.active) as item (item.name)}
                        <option value={item.name}>{displayName(item)}</option>
                      {/each}
                    </select>
                    <button
                      class="btn primary"
                      disabled={busy === k || !pickedModel[k]}
                      onclick={() => doStart(m.id, entry)}
                    >
                      ▶ Start
                    </button>
                  </div>
                {:else}
                  <div class="muted">no models configured</div>
                {/if}
              {:else}
                <div class="error">{entry.error || 'unreachable'}</div>
              {/if}
            </div>
          {/each}
          <div class="row end">
            <a class="open" href={`#/machine/${m.id}`}>Open →</a>
          </div>
        </div>
      {:else}
        {@const st = $statuses[m.id] ?? { state: 'checking' }}
        <div class="card machine">
          <div class="row">
            <span class="dot {st.state}"></span>
            <a class="name" href={`#/machine/${m.id}`}>{m.name}</a>
            <span class="badge {st.state}">{st.state}</span>
          </div>

          {#if st.state === 'running'}
            <div class="model">{st.model}</div>
            <div class="muted">running</div>
            {#if confirmStop === m.id}
              <div class="confirm">
                <span>Stop {st.model}?</span>
                <button class="btn danger" disabled={busy === m.id} onclick={() => doStopLocal(m.id, st.model!)}>
                  Yes, stop
                </button>
                <button class="btn" onclick={() => (confirmStop = null)}>Cancel</button>
              </div>
            {:else}
              <div class="row end">
                <button class="btn danger" disabled={busy === m.id} onclick={() => (confirmStop = m.id)}>
                  ■ Stop
                </button>
              </div>
            {/if}
          {:else if st.state === 'idle'}
            <div class="muted">no model running</div>
          {:else if st.state === 'offline'}
            <div class="error">{st.error || 'unreachable'}</div>
          {:else}
            <div class="muted">checking…</div>
          {/if}

          <div class="row end">
            <a class="open" href={`#/machine/${m.id}`}>Open →</a>
          </div>
        </div>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .machine .name {
    font-weight: 600;
    color: var(--text);
  }

  .machine .model {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.95rem;
    margin: 0.45rem 0 0.1rem;
  }

  .coordinator {
    border-left: 3px solid var(--accent, #4a7dff);
  }

  .badge.fleet {
    margin-left: auto;
  }

  .entry {
    border-top: 1px solid var(--border, #e5e5e5);
    margin-top: 0.5rem;
    padding-top: 0.5rem;
  }

  .entry-name {
    font-weight: 600;
  }

  .small {
    font-size: 0.8rem;
  }

  .startrow {
    gap: 0.5rem;
    margin-top: 0.4rem;
  }

  .select {
    flex: 1;
    min-width: 0;
  }

  .open {
    font-size: 0.85rem;
  }
</style>
