import { writable } from 'svelte/store';
import { apiFetch, apiJson } from '$lib/api';
import { readNDJSON } from '$lib/ndjson';
import { doctorStatus } from '$lib/doctorStatus';

// Mirrors cli.DoctorReport: the machine-wide `lerd doctor`, as opposed to the
// per-site doctor in ./doctor. A fix's target never leaves the server; the
// panel names a fix by its key and the finding it belongs to.
export interface MachineFix {
  tier: 'auto' | 'manual' | 'none';
  key?: string;
  label?: string;
  // Why a heavy fix asks first, empty for one that just runs.
  confirm?: string;
}

export interface MachineFinding {
  section?: string;
  name: string;
  status: 'ok' | 'fail' | 'warn' | 'info';
  message?: string;
  hint?: string;
  fix?: MachineFix;
}

export interface MachineReport {
  version: string;
  findings: MachineFinding[];
  failures: number;
  warnings: number;
}

// The last report, for the System list's status dot.
export const machineReport = writable<MachineReport | null>(null);

export async function loadMachineReport(): Promise<MachineReport> {
  const r = await apiJson<MachineReport & { error?: string }>('/api/doctor');
  if (r.error) throw new Error(r.error);
  const report = { ...r, findings: r.findings ?? [] };
  machineReport.set(report);
  return report;
}

interface FixEvent {
  phase: 'output' | 'done' | 'failed';
  line?: string;
  error?: string;
}

// applyMachineFix runs one automatic fix, handing each output line to onLine.
export async function applyMachineFix(
  f: MachineFinding,
  onLine: (line: string) => void
): Promise<{ ok: boolean; error?: string }> {
  const res = await apiFetch('/api/doctor/fix', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ key: f.fix?.key, name: f.name })
  });
  if (!res.ok || !res.body) {
    const text = (await res.text().catch(() => '')).trim();
    return { ok: false, error: text || `${res.status} ${res.statusText}` };
  }
  let result: { ok: boolean; error?: string } = { ok: false, error: 'the fix ended without a result' };
  await readNDJSON<FixEvent>(res.body, (e) => {
    if (e.phase === 'output' && e.line !== undefined) onLine(e.line);
    else if (e.phase === 'done') result = { ok: true };
    else if (e.phase === 'failed') result = { ok: false, error: e.error };
  });
  return result;
}

// partitionFindings splits a report into what needs attention (failures
// first), optional repairs offered on informational findings, and what passed.
export function partitionFindings(findings: MachineFinding[]) {
  const problems = findings
    .filter((f) => f.status === 'fail' || f.status === 'warn')
    .sort((a, b) => doctorStatus(a.status).rank - doctorStatus(b.status).rank);
  const optional = findings.filter((f) => f.status === 'info' && f.fix?.tier === 'auto');
  const passed = findings.filter((f) => f.status === 'ok');
  return { problems, optional, passed };
}
