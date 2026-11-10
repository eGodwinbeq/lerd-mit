<script lang="ts">
  import { m } from '../paraglide/messages.js';

  // The Fix button on a doctor finding: red for a failure, amber otherwise,
  // with a spinner while this finding's fix runs.
  interface Props {
    status: string;
    running: boolean;
    disabled?: boolean;
    onclick: () => void;
  }
  let { status, running, disabled = false, onclick }: Props = $props();
</script>

<button
  type="button"
  {onclick}
  {disabled}
  class="shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-semibold shadow-sm disabled:opacity-50 transition-colors {status === 'fail' ? 'bg-lerd-red hover:bg-lerd-redhov text-lerd-onred' : 'bg-amber-500 hover:bg-amber-600 text-white'}"
>
  {#if running}
    <svg class="w-3.5 h-3.5 animate-spin" viewBox="0 0 24 24" fill="none">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" />
      <path class="opacity-90" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.4 0 0 5.4 0 12h4z" />
    </svg>
    {m.sites_doctor_fixing()}
  {:else}
    <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
    </svg>
    {m.sites_doctor_fix()}
  {/if}
</button>
