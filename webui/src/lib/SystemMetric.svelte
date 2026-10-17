<script lang="ts">
  import Icon from './Icon.svelte'
  import MetricChart from './MetricChart.svelte'

  let {
    label,
    icon,
    value,
    detail,
    color,
    values = [],
    available = true,
    note = 'No telemetry available',
  }: {
    label: string
    icon: string
    value: string
    detail: string
    color: string
    values?: number[]
    available?: boolean
    note?: string
  } = $props()
</script>

<section class="metric" style={`--metric-color: ${color}`}>
  <div class="metric-top">
    <span class="metric-icon"><Icon name={icon} size={15} /></span>
    <span class="metric-label">{label}</span>
    <span class="metric-value" class:unavailable={!available}>{value}</span>
  </div>

  {#if available}
    <MetricChart {values} label={`${label} usage history`} />
  {:else}
    <div class="metric-blank">{note}</div>
  {/if}

  <p class="metric-detail" title={detail}>{detail}</p>
</section>

<style>
  .metric {
    min-width: 0;
    padding: 0.8rem 0.85rem 0.7rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--panel-2);
  }

  .metric-top {
    display: flex;
    align-items: center;
    gap: 0.38rem;
  }

  .metric-icon {
    display: grid;
    place-items: center;
    color: var(--metric-color);
  }

  .metric-label {
    font-size: 0.72rem;
    font-weight: 650;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .metric-value {
    margin-left: auto;
    font-size: 1.22rem;
    font-weight: 650;
    letter-spacing: -0.03em;
    font-variant-numeric: tabular-nums;
    color: var(--text);
    line-height: 1.1;
  }

  .metric-value.unavailable {
    color: var(--faint);
  }

  .metric-blank {
    display: grid;
    place-items: center;
    height: 72px;
    margin-top: 0.6rem;
    border: 1px dashed var(--border-strong);
    border-radius: 6px;
    font-size: 0.74rem;
    color: var(--faint);
    text-align: center;
    padding: 0 0.4rem;
  }

  .metric-detail {
    margin: 0.5rem 0 0;
    font-size: 0.74rem;
    line-height: 1.35;
    color: var(--faint);
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
</style>
