<script lang="ts">
  import { toasts } from './toast'
</script>

<div class="toasts">
  {#each $toasts as t (t.id)}
    <div class="toast {t.kind}">{t.text}</div>
  {/each}
</div>
