<script lang="ts">
  import { machines, refreshMachine, statuses } from '../lib/store'
  import { stopModel } from '../lib/api'
  import { toast } from '../lib/toast'

  let { onopenSettings }: { onopenSettings: () => void } = $props()

  let confirmStop = $state<string | null>(null)
  let busy = $state<string | null>(null)

  async function doStop(id: string, model: string) {
    const m = $machines.find((x) => x.id === id)
    if (!m || !model) return
    busy = id
    try {
      const res = await stopModel(m, model)
      toast('ok', res.message || `Stopped ${model} on ${m.name}`)
    } catch (e) {
      toast('err', `Stop failed: ${e instanceof Error ? e.message : e}`)
    } finally {
      busy = null
      confirmStop = null
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
              <button class="btn danger" disabled={busy === m.id} onclick={() => doStop(m.id, st.model!)}>
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

  .open {
    font-size: 0.85rem;
  }
</style>
