<script lang="ts">
  import Icon from './Icon.svelte'
  import { toasts } from './toast'
</script>

<div class="toasts">
  {#each $toasts as t (t.id)}
    <div class="toast {t.kind}" role="status">
      <span class="toast-icon">
        <Icon name={t.kind === 'ok' ? 'checkCircle' : 'alert'} size={17} />
      </span>
      <span>{t.text}</span>
    </div>
  {/each}
</div>
