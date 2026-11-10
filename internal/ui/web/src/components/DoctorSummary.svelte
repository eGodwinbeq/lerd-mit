<script lang="ts">
  import { DOCTOR_STATUS } from '$lib/doctorStatus';
  import { m } from '../paraglide/messages.js';

  // The headline of a doctor run: the overall verdict, a subtitle, and a pill
  // per result count, so the outcome reads before any scrolling.
  interface Props {
    loading: boolean;
    // False until a report exists, so an empty start never reads as all clear.
    ready?: boolean;
    failures: number;
    warnings: number;
    ok: number;
    subtitle?: string;
  }
  let { loading, ready = true, failures, warnings, ok, subtitle = '' }: Props = $props();

  const allClear = $derived(!loading && ready && failures === 0 && warnings === 0);
</script>

<div
  class="flex items-center gap-3 rounded-xl border p-3.5 {allClear
    ? 'border-emerald-500/20 bg-emerald-500/5'
    : failures > 0
      ? 'border-red-500/20 bg-red-500/5'
      : 'border-amber-500/20 bg-amber-500/5'}"
>
  <span
    class="grid place-items-center w-10 h-10 rounded-full shrink-0 {allClear
      ? 'bg-emerald-500/15 text-emerald-500'
      : failures > 0
        ? 'bg-red-500/15 text-red-500'
        : 'bg-amber-500/15 text-amber-500'}"
  >
    {#if loading}
      <svg class="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" />
        <path class="opacity-90" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.4 0 0 5.4 0 12h4z" />
      </svg>
    {:else}
      <svg class="w-6 h-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d={allClear ? DOCTOR_STATUS.ok.icon : failures > 0 ? DOCTOR_STATUS.fail.icon : DOCTOR_STATUS.warn.icon}
        />
      </svg>
    {/if}
  </span>
  <div class="min-w-0 flex-1">
    <p class="text-sm font-semibold text-gray-900 dark:text-white">
      {loading ? m.sites_doctor_running() : allClear ? m.sites_doctor_allClear() : m.sites_doctor_summary({ failures, warnings })}
    </p>
    {#if subtitle}
      <p class="text-[11px] text-gray-500 dark:text-gray-400 truncate">{subtitle}</p>
    {/if}
  </div>
  {#if ready}
    <div class="hidden sm:flex items-center gap-1.5 shrink-0">
      {#each [{ n: failures, s: DOCTOR_STATUS.fail }, { n: warnings, s: DOCTOR_STATUS.warn }, { n: ok, s: DOCTOR_STATUS.ok }] as pill, i (i)}
        {#if pill.n > 0}
          <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-semibold {pill.s.badge}">
            <span class="w-1.5 h-1.5 rounded-full {pill.s.dot}"></span>{pill.n}
          </span>
        {/if}
      {/each}
    </div>
  {/if}
</div>
