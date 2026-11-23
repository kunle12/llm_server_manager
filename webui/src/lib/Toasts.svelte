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
      <span class="toast-copy">
        <span class="toast-kind">{t.kind === 'ok' ? 'confirmed' : 'fault'}</span>
        <span>{t.text}</span>
      </span>
    </div>
  {/each}
</div>
