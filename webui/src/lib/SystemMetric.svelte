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
    position: relative;
    min-width: 0;
    padding: 0.75rem 0.8rem 0.65rem 1rem;
    border: 1px solid var(--border);
    border-radius: 0 12px 12px 0;
    background: rgba(255, 255, 255, 0.34);
    overflow: hidden;
  }

  :global(:root[data-theme='dark']) .metric {
    background: rgba(255, 255, 255, 0.025);
  }

  .metric::before {
    content: '';
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 5px;
    background: var(--metric-color);
  }

  .metric-top {
    display: flex;
    align-items: baseline;
    gap: 0.38rem;
  }

  .metric-icon {
    display: grid;
    place-items: center;
    align-self: center;
    color: var(--metric-color);
  }

  .metric-label {
    font-family: var(--mono);
    font-size: 7.5px;
    font-weight: 500;
    letter-spacing: 0.18em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .metric-value {
    margin-left: auto;
    font-family: var(--mono);
    font-size: 1.12rem;
    font-weight: 500;
    letter-spacing: -0.02em;
    font-variant-numeric: tabular-nums;
    color: var(--text);
    line-height: 1.1;
  }

  .metric-value.unavailable {
    color: var(--dim);
  }

  .metric-blank {
    display: grid;
    place-items: center;
    height: 72px;
    margin-top: 0.6rem;
    border: 1px dashed var(--border-strong);
    border-radius: 8px;
    font-family: var(--mono);
    font-size: 7.5px;
    letter-spacing: 0.08em;
    color: var(--dim);
    text-align: center;
    padding: 0 0.4rem;
  }

  .metric-detail {
    margin: 0.5rem 0 0;
    font-family: var(--mono);
    font-size: 7.5px;
    line-height: 1.45;
    letter-spacing: 0.02em;
    color: var(--dim);
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
</style>
