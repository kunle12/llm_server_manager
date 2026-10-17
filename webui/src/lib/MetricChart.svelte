<script lang="ts">
  /**
   * Compact bar-history chart (task-manager style). Bars are right-aligned so
   * a freshly opened page fills in as samples arrive, and empty slots keep the
   * chart's footprint stable.
   */
  let {
    values = [],
    slots = 48,
    max = 100,
    label = 'usage history',
  }: { values?: number[]; slots?: number; max?: number; label?: string } = $props()

  const bars = $derived.by(() => {
    const out: (number | null)[] = new Array(slots).fill(null)
    const trimmed = values.slice(-slots)
    const offset = slots - trimmed.length
    for (let i = 0; i < trimmed.length; i++) {
      const pct = (trimmed[i] / max) * 100
      out[offset + i] = Math.min(100, Math.max(0, pct))
    }
    return out
  })
</script>

<div class="chart" role="img" aria-label={label}>
  {#each bars as value, i (i)}
    <span class="slot">
      {#if value !== null}
        <span class="bar" style={`height: ${Math.max(2, value)}%`}></span>
      {/if}
    </span>
  {/each}
</div>

<style>
  .chart {
    display: flex;
    align-items: stretch;
    gap: 2px;
    width: 100%;
    height: 72px;
    margin-top: 0.6rem;
  }

  .slot {
    position: relative;
    flex: 1;
    min-width: 1px;
    overflow: hidden;
    border-radius: 3px;
    /* Fallback for browsers without color-mix(); the tinted value wins there. */
    background: var(--panel-3);
    background: color-mix(in srgb, var(--metric-color, var(--accent)) 7%, transparent);
  }

  .bar {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    border-radius: 3px 3px 1px 1px;
    background: var(--metric-color, var(--accent));
    background: linear-gradient(
      to top,
      color-mix(in srgb, var(--metric-color, var(--accent)) 55%, transparent),
      var(--metric-color, var(--accent))
    );
    transition: height 0.35s cubic-bezier(0.2, 0.9, 0.3, 1);
  }

  @media (prefers-reduced-motion: reduce) {
    .bar {
      transition: none;
    }
  }
</style>
