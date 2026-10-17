<script lang="ts">
  import Icon from './Icon.svelte'
  import SystemMetric from './SystemMetric.svelte'
  import type { SystemPoint, SystemSnapshot } from './types'

  /**
   * Three live metric tiles for one fleet entry (local machine or worker).
   * The parent aligns this block with that entry's machine info.
   */
  let {
    snapshot,
    history = [],
    error,
    offline = false,
  }: {
    snapshot?: SystemSnapshot | null
    history?: SystemPoint[]
    error?: string
    offline?: boolean
  } = $props()

  const cpuSeries = $derived(history.map((p) => p.cpu))
  const memSeries = $derived(history.map((p) => p.mem))
  // Drop unavailable-GPU samples instead of drawing them as 0%.
  const gpuSeries = $derived(history.filter((p) => p.gpu !== null).map((p) => p.gpu as number))

  const cpuDetail = $derived.by(() => {
    if (!snapshot) return ''
    const parts = [`${snapshot.cpu.cores} ${snapshot.cpu.cores === 1 ? 'core' : 'cores'}`]
    if (snapshot.cpu.model) parts.push(snapshot.cpu.model)
    return parts.join(' · ')
  })

  const memoryDetail = $derived(
    snapshot
      ? `${formatBytes(snapshot.memory.used_bytes)} of ${formatBytes(snapshot.memory.total_bytes)}`
      : '',
  )

  const gpuDetail = $derived.by(() => {
    const gpu = snapshot?.gpu
    if (!snapshot || !gpu?.available) return 'No telemetry source detected'
    const parts: string[] = []
    if (gpu.memory_total_bytes > 0) {
      parts.push(`${formatBytes(gpu.memory_used_bytes)} of ${formatBytes(gpu.memory_total_bytes)}`)
    } else if (gpu.memory_used_bytes > 0) {
      parts.push(`${formatBytes(gpu.memory_used_bytes)} in use`)
    }
    if (gpu.name) {
      parts.push(gpu.core_count ? `${gpu.name} · ${gpu.core_count}-core GPU` : gpu.name)
    } else if ((gpu.count ?? 0) > 1) {
      parts.push(`${gpu.count} GPUs`)
    }
    return parts.join(' · ') || 'GPU telemetry'
  })

  function formatPct(value?: number | null): string {
    if (value == null || Number.isNaN(value)) return '—'
    return `${value < 10 ? value.toFixed(1) : Math.round(value)}%`
  }

  function formatBytes(bytes?: number): string {
    if (bytes == null || bytes < 0) return '—'
    if (bytes === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let value = bytes
    let unit = 0
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024
      unit++
    }
    return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`
  }

  function friendlyError(message?: string): string {
    if (!message) return 'The machine did not return system data.'
    if (/404|not found/i.test(message)) {
      return "This manager doesn't report system stats yet — update it to the latest version."
    }
    if (/timeout/i.test(message)) return 'Timed out while reading system stats.'
    if (/failed to fetch|load failed|network ?error/i.test(message)) {
      return "Can't reach this machine right now."
    }
    return message
  }
</script>

{#if offline}
  <div class="metrics-note">
    <div class="note-icon"><Icon name="alert" size={17} /></div>
    <span>Machine offline — no telemetry</span>
  </div>
{:else if snapshot === undefined && !error}
  <div class="metrics" aria-label="Loading system stats">
    {#each [0, 1, 2] as i (i)}
      <section class="metric skeleton" aria-hidden="true">
        <span class="sk sk-top"></span>
        <span class="sk sk-chart"></span>
        <span class="sk sk-detail"></span>
      </section>
    {/each}
  </div>
{:else if snapshot === null}
  <div class="metrics-note">
    <div class="note-icon"><Icon name="alert" size={17} /></div>
    <div>
      <strong>System stats unavailable</strong>
      <p>{friendlyError(error)}</p>
    </div>
  </div>
{:else if snapshot}
  <div class="metrics">
    <SystemMetric
      label="CPU"
      icon="cpu"
      value={formatPct(snapshot.cpu.usage_percent)}
      detail={cpuDetail}
      color="var(--chart-cpu)"
      values={cpuSeries}
    />
    <SystemMetric
      label="GPU"
      icon="gpu"
      value={snapshot.gpu?.available ? formatPct(snapshot.gpu.usage_percent) : '—'}
      detail={gpuDetail}
      color="var(--chart-gpu)"
      values={gpuSeries}
      available={!!snapshot.gpu?.available}
      note="No GPU telemetry"
    />
    <SystemMetric
      label="Memory"
      icon="memory"
      value={formatPct(snapshot.memory.usage_percent)}
      detail={memoryDetail}
      color="var(--chart-mem)"
      values={memSeries}
    />
  </div>
{/if}

<style>
  .metrics {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 0.6rem;
    align-content: start;
  }

  @media (max-width: 560px) {
    .metrics {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .metrics > :global(:last-child) {
      grid-column: 1 / -1;
    }
  }

  /* Very narrow phones: one full-width metric per row. */
  @media (max-width: 359px) {
    .metrics {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  .metrics-note {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.6rem;
    min-height: 8.6rem;
    padding: 1rem;
    border: 1px dashed var(--border-strong);
    border-radius: var(--radius-sm);
    color: var(--muted);
    text-align: left;
  }

  .metrics-note strong {
    display: block;
    font-size: 0.84rem;
    color: var(--text);
  }

  .metrics-note p {
    margin: 0.2rem 0 0;
    font-size: 0.78rem;
    max-width: 26rem;
  }

  .note-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 30px;
    height: 30px;
    border-radius: 9px;
    background: var(--panel-3);
    color: var(--faint);
  }

  .metric.skeleton {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
    min-height: 8.6rem;
    padding: 0.8rem 0.85rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--panel-2);
  }

  .sk {
    display: block;
    border-radius: 5px;
    /* Fallback first for browsers without color-mix(). */
    background: var(--panel-3);
    background: linear-gradient(
      90deg,
      var(--panel-3) 25%,
      color-mix(in srgb, var(--panel-3) 55%, var(--panel)) 37%,
      var(--panel-3) 63%
    );
    background-size: 400% 100%;
    animation: shimmer 1.4s ease infinite;
  }

  .sk-top {
    width: 55%;
    height: 14px;
  }

  .sk-chart {
    flex: 1;
    min-height: 68px;
  }

  .sk-detail {
    width: 75%;
    height: 10px;
  }

  @keyframes shimmer {
    0% {
      background-position: 100% 0;
    }
    100% {
      background-position: -100% 0;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .sk {
      animation: none;
    }
  }
</style>
