<script lang="ts">
  import { onMount } from 'svelte';
  import DetailHeader from '$components/DetailHeader.svelte';
  import DetailButton from '$components/DetailButton.svelte';
  import DoctorSummary from '$components/DoctorSummary.svelte';
  import DoctorFixButton from '$components/DoctorFixButton.svelte';
  import DoctorResultIcon from '$components/DoctorResultIcon.svelte';
  import CopyButton from '$components/CopyButton.svelte';
  import ConfirmModal from '$components/ConfirmModal.svelte';
  import { accessMode } from '$stores/accessMode';
  import {
    applyMachineFix,
    loadMachineReport,
    partitionFindings,
    type MachineFinding,
    type MachineReport
  } from '$stores/troubleshoot';
  import { m } from '../../paraglide/messages.js';

  let report = $state<MachineReport | null>(null);
  let loading = $state(false);
  let error = $state('');
  // The finding whose fix is running, so only its button spins (one at a time).
  let fixing = $state('');
  let output = $state<{ title: string; lines: string[]; error: string } | null>(null);
  let confirming = $state<MachineFinding | null>(null);

  const parts = $derived(partitionFindings(report?.findings ?? []));

  async function run() {
    loading = true;
    error = '';
    try {
      report = await loadMachineReport();
    } catch (e) {
      error = e instanceof Error ? e.message : m.common_loadFailed();
    } finally {
      loading = false;
    }
  }

  onMount(run);

  function requestFix(f: MachineFinding) {
    if (f.fix?.confirm) confirming = f;
    else void fix(f);
  }

  async function fix(f: MachineFinding) {
    confirming = null;
    fixing = f.name;
    const out = { title: f.fix?.label ?? f.name, lines: [] as string[], error: '' };
    output = out;
    try {
      const res = await applyMachineFix(f, (line) => {
        out.lines = [...out.lines, line];
        output = { ...out };
      });
      if (!res.ok) output = { ...out, error: res.error ?? '' };
    } catch (e) {
      output = { ...out, error: e instanceof Error ? e.message : String(e) };
    } finally {
      fixing = '';
    }
    await run();
  }

  const canFix = (f: MachineFinding) => f.fix?.tier === 'auto' && Boolean(f.fix.key) && $accessMode.localControl;
  // A manual repair names its command in the fix label, else in the hint.
  const manualStep = (f: MachineFinding) => (f.fix?.tier === 'manual' ? f.fix.label || f.hint || '' : '');

  const statusLabel = (s: string): string => {
    switch (s) {
      case 'ok':
        return m.sites_doctor_status_ok();
      case 'warn':
        return m.sites_doctor_status_warn();
      case 'fail':
        return m.sites_doctor_status_fail();
      default:
        return m.sites_doctor_status_unknown();
    }
  };
</script>

{#snippet findingRow(f: MachineFinding)}
  <div
    data-finding={f.name}
    class="flex items-start gap-3 px-3.5 py-2.5 {f.status === 'fail' ? 'bg-red-500/[0.04]' : f.status === 'warn' ? 'bg-amber-500/[0.03]' : ''}"
  >
    <div class="min-w-0 flex-1">
      <div class="flex items-center gap-2">
        <span class="text-sm font-medium text-gray-900 dark:text-white">{f.name}</span>
        {#if f.section}
          <span class="text-[10px] uppercase tracking-wide text-gray-400 dark:text-gray-500">{f.section}</span>
        {/if}
      </div>
      {#if f.message}
        <p class="mt-0.5 text-[11px] leading-snug text-gray-500 dark:text-gray-400 break-words">{f.message}</p>
      {/if}
      {#if f.hint && f.fix?.tier !== 'manual'}
        <p class="mt-0.5 text-[11px] leading-snug text-gray-500 dark:text-gray-400 break-words">{f.hint}</p>
      {/if}
      {#if manualStep(f)}
        <div class="mt-1.5 flex items-center gap-2 rounded-md bg-gray-100 dark:bg-white/5 px-2 py-1">
          <span class="text-[10px] font-semibold uppercase tracking-wide text-gray-400 shrink-0">{m.system_troubleshoot_runYourself()}</span>
          <code class="min-w-0 flex-1 text-[11px] text-gray-700 dark:text-gray-300 break-words">{manualStep(f)}</code>
          <CopyButton text={manualStep(f)} label={m.common_copy()} tone="faint" />
        </div>
      {/if}
    </div>
    {#if canFix(f)}
      <DoctorFixButton status={f.status} running={fixing === f.name} disabled={loading || Boolean(fixing)} onclick={() => requestFix(f)} />
    {/if}
    <DoctorResultIcon status={f.status} label={statusLabel(f.status)} />
  </div>
{/snippet}

<div class="flex-1 overflow-y-auto">
  <DetailHeader title={m.system_troubleshoot_title()}>
    {#snippet trailing()}
      <DetailButton onclick={run} disabled={loading || Boolean(fixing)} loading={loading}>
        {loading ? m.sites_doctor_running() : m.sites_doctor_refresh()}
      </DetailButton>
    {/snippet}
  </DetailHeader>

  <div class="p-3 space-y-3">
    <p class="text-xs text-gray-500 dark:text-gray-400">{m.system_troubleshoot_description()}</p>

    <DoctorSummary
      {loading}
      ready={Boolean(report)}
      failures={report?.failures ?? 0}
      warnings={report?.warnings ?? 0}
      ok={parts.passed.length}
      subtitle={report ? `lerd ${report.version}` : ''}
    />

    {#if error}
      <div class="rounded-lg border border-red-500/20 bg-red-500/5 p-3 text-xs text-red-400">{error}</div>
    {/if}

    {#if parts.problems.length > 0}
      <div class="rounded-xl border border-gray-200/80 dark:border-lerd-border divide-y divide-gray-100 dark:divide-lerd-border overflow-hidden">
        {#each parts.problems as f, i (i)}
          {@render findingRow(f)}
        {/each}
      </div>
    {/if}

    {#if parts.optional.length > 0}
      <h3 class="text-[11px] font-semibold uppercase tracking-wide text-gray-400">{m.system_troubleshoot_optional()}</h3>
      <div class="rounded-xl border border-gray-200/80 dark:border-lerd-border divide-y divide-gray-100 dark:divide-lerd-border overflow-hidden">
        {#each parts.optional as f, i (i)}
          {@render findingRow(f)}
        {/each}
      </div>
    {/if}

    {#if output}
      <div class="rounded-xl border border-gray-200/80 dark:border-lerd-border overflow-hidden">
        <div class="px-3.5 py-2 text-[11px] font-semibold text-gray-500 dark:text-gray-400 border-b border-gray-100 dark:border-lerd-border">
          {m.system_troubleshoot_output()}: {output.title}
        </div>
        <pre class="px-3.5 py-2 text-[11px] leading-snug text-gray-700 dark:text-gray-300 whitespace-pre-wrap break-words max-h-48 overflow-y-auto">{#each output.lines as line, i (i)}<div>{line}</div>{/each}</pre>
        {#if output.error}
          <p class="px-3.5 pb-2 text-[11px] text-red-400 break-words">{output.error}</p>
        {/if}
      </div>
    {/if}

    {#if parts.passed.length > 0}
      <details class="rounded-xl border border-gray-200/80 dark:border-lerd-border">
        <summary class="cursor-pointer px-3.5 py-2 text-xs text-gray-500 dark:text-gray-400">
          {m.system_troubleshoot_passed({ count: parts.passed.length })}
        </summary>
        <ul class="px-3.5 pb-2 space-y-0.5">
          {#each parts.passed as f, i (i)}
            <li class="text-[11px] text-gray-500 dark:text-gray-400">
              <span class="text-gray-400 dark:text-gray-500">{f.section}</span> · {f.name}
            </li>
          {/each}
        </ul>
      </details>
    {/if}
  </div>
</div>

<ConfirmModal
  open={Boolean(confirming)}
  title={m.system_troubleshoot_confirmTitle()}
  body={confirming ? `${confirming.fix?.label ?? confirming.name}. ${confirming.fix?.confirm ?? ''}` : ''}
  confirmLabel={m.sites_doctor_fix()}
  onconfirm={() => confirming && fix(confirming)}
  onclose={() => (confirming = null)}
/>
