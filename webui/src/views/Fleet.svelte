<script lang="ts">
  import Icon from '../lib/Icon.svelte'
  import ProxySwitch from '../lib/ProxySwitch.svelte'
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

  /**
   * Models that can be started, sorted by display name. The server may return
   * them in any order (its model map iterates randomly), so sorting here keeps
   * the dropdown stable between polls and across a worker on an older binary.
   */
  function selectable(entry: FleetMachine): ModelItem[] {
    return (entry.models ?? [])
      .filter((x) => !x.active)
      .sort((a, b) => displayName(a).localeCompare(displayName(b)))
  }

  /** Map raw browser fetch failures to plain language (single-machine view). */
  function friendlyError(message?: string): string {
    if (!message) return 'Unreachable'
    if (/failed to fetch|load failed|network ?error/i.test(message)) {
      return "Can't reach the machine — check it's running and the URL is correct"
    }
    if (/timeout/i.test(message)) return 'Timed out'
    return message
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

<div class="page-head">
  <div>
    <h1>Fleet</h1>
    <p class="sub">Monitor and control every model across your machines.</p>
  </div>
</div>

{#if $machines.length === 0}
  <div class="card empty">
    <div class="empty-icon"><Icon name="server" size={26} /></div>
    <h2>No machines yet</h2>
    <p class="muted">
      Add an llm_server_manager instance to see and control its models. The manager serving this
      page is added automatically.
    </p>
    <button class="btn primary" onclick={onopenSettings}>
      <Icon name="plus" size={16} /> Add a machine
    </button>
  </div>
{:else}
  <div class="grid">
    {#each $machines as m (m.id)}
      {@const fv = $fleetViews[m.id]}
      {#if fv === undefined}
        <article class="card machine">
          <div class="machine-head">
            <div class="machine-title">
              <span class="dot checking"></span>
              <a class="machine-name" href={`#/machine/${m.id}`}>{m.name}</a>
            </div>
            <span class="spacer"></span>
            <span class="pill checking">Checking</span>
          </div>
          <div class="entry"><p class="muted">Contacting {m.name}…</p></div>
        </article>
      {:else if fv !== null}
        <article class="card machine">
          <div class="machine-head">
            <div class="machine-title">
              <a class="machine-name" href={`#/machine/${m.id}`}>{m.name}</a>
            </div>
            <span class="spacer"></span>
            <span class="pill fleet"><Icon name="bolt" size={12} /> Coordinator</span>
          </div>

          <ul class="entries">
            {#each fv as entry (entry.machine)}
              {@const k = key(m.id, entry.machine)}
              <li class="entry">
                <div class="entry-head">
                  <span class="dot {entry.status}"></span>
                  <span class="entry-name">{entry.machine}</span>
                  {#if isLocal(entry)}<span class="tag">this machine</span>{/if}
                </div>

                {#if entry.status === 'online'}
                  {#if entry.running}
                    <div class="entry-body">
                      <span class="entry-model">{entry.running.alias || entry.running.name}</span>
                      {#if entry.running.port}
                        <span class="muted">running on port {entry.running.port}</span>
                      {:else}
                        <span class="muted">running</span>
                      {/if}
                    </div>
                    {#if confirmStop === k}
                      <div class="confirm">
                        <span>Stop {entry.running.alias || entry.running.name}?</span>
                        <button
                          class="btn danger"
                          disabled={busy === k}
                          onclick={() => doStop(m.id, entry, entry.running!.name)}
                        >
                          Stop
                        </button>
                        <button class="btn subtle" onclick={() => (confirmStop = null)}>Cancel</button>
                      </div>
                    {:else}
                      <div class="row end">
                        <button
                          class="btn danger"
                          disabled={busy === k}
                          onclick={() => (confirmStop = k)}
                        >
                          <Icon name="stop" size={16} /> Stop
                        </button>
                      </div>
                    {/if}
                  {:else if selectable(entry).length > 0}
                    <div class="row startrow">
                      <select
                        class="select"
                        value={pickedModel[k] ?? ''}
                        onchange={(e) =>
                          (pickedModel = {
                            ...pickedModel,
                            [k]: (e.currentTarget as HTMLSelectElement).value,
                          })}
                      >
                        <option value="">Start a model…</option>
                        {#each selectable(entry) as item (item.name)}
                          <option value={item.name}>{displayName(item)}</option>
                        {/each}
                      </select>
                      <button
                        class="btn primary"
                        disabled={busy === k || !pickedModel[k]}
                        onclick={() => doStart(m.id, entry)}
                      >
                        <Icon name="play" size={13} /> Start
                      </button>
                    </div>
                  {:else}
                    <p class="muted entry-note">No models configured</p>
                  {/if}
                {:else}
                  <p class="entry-error">{friendlyError(entry.error)}</p>
                {/if}
              </li>
            {/each}
          </ul>

          <div class="machine-foot">
            <a class="link-btn" href={`#/machine/${m.id}`}>
              Manage models <Icon name="chevronRight" size={14} />
            </a>
            <span class="spacer"></span>
            <ProxySwitch machine={m} compact />
            <a class="link-btn subtle" href={`#/machine/${m.id}/config`}>
              <Icon name="sliders" size={14} /> Config
            </a>
          </div>
        </article>
      {:else}
        {@const st = $statuses[m.id] ?? { state: 'checking' }}
        <article class="card machine">
          <div class="machine-head">
            <div class="machine-title">
              <span class="dot {st.state}"></span>
              <a class="machine-name" href={`#/machine/${m.id}`}>{m.name}</a>
            </div>
            <span class="spacer"></span>
            <span class="pill {st.state}">{st.state}</span>
          </div>

          <div class="entry">
            {#if st.state === 'running'}
              <div class="entry-body">
                <span class="entry-model">{st.model}</span>
                <span class="muted">running</span>
              </div>
              {#if confirmStop === m.id}
                <div class="confirm">
                  <span>Stop {st.model}?</span>
                  <button
                    class="btn danger"
                    disabled={busy === m.id}
                    onclick={() => doStopLocal(m.id, st.model!)}
                  >
                    Stop
                  </button>
                  <button class="btn subtle" onclick={() => (confirmStop = null)}>Cancel</button>
                </div>
              {:else}
                <div class="row end">
                  <button
                    class="btn danger"
                    disabled={busy === m.id}
                    onclick={() => (confirmStop = m.id)}
                  >
                    <Icon name="stop" size={16} /> Stop
                  </button>
                </div>
              {/if}
            {:else if st.state === 'idle'}
              <p class="muted entry-note">No model running</p>
            {:else if st.state === 'offline'}
              <p class="entry-error">{friendlyError(st.error)}</p>
            {:else}
              <p class="muted entry-note">Checking…</p>
            {/if}
          </div>

          <div class="machine-foot">
            <a class="link-btn" href={`#/machine/${m.id}`}>
              Manage models <Icon name="chevronRight" size={14} />
            </a>
            <span class="spacer"></span>
            <ProxySwitch machine={m} compact />
            <a class="link-btn subtle" href={`#/machine/${m.id}/config`}>
              <Icon name="sliders" size={14} /> Config
            </a>
          </div>
        </article>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .entry-note {
    margin: 0.45rem 0 0;
    font-size: 0.88rem;
  }
</style>
