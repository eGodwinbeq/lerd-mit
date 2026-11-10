<script lang="ts">
  import { doctorStatus } from '$lib/doctorStatus';

  // A doctor finding's result, as a check / warning / error / info icon.
  interface Props {
    status: string;
    label: string;
  }
  let { status, label }: Props = $props();
  const s = $derived(doctorStatus(status));
</script>

<span class="shrink-0 {s.text}" title={label} aria-label={label}>
  <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path stroke-linecap="round" stroke-linejoin="round" d={s.icon} />
  </svg>
</span>
