import { describe, it, expect, vi, afterEach } from 'vitest';
import { get } from 'svelte/store';
import { applyMachineFix, loadMachineReport, machineReport, partitionFindings, type MachineFinding } from './troubleshoot';

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
  machineReport.set(null);
});

function mockFetch(body: string, init: ResponseInit = { status: 200 }) {
  const fn = vi.fn().mockResolvedValue(new Response(body, init));
  globalThis.fetch = fn;
  return fn;
}

const finding = (over: Partial<MachineFinding>): MachineFinding => ({ name: 'x', status: 'ok', ...over });

describe('partitionFindings', () => {
  it('lists failures before warnings, keeps optional fixes apart and counts what passed', () => {
    const parts = partitionFindings([
      finding({ name: 'tray', status: 'warn' }),
      finding({ name: 'dns rule', status: 'fail' }),
      finding({ name: 'agent', status: 'ok' }),
      finding({ name: 'disk', status: 'info', fix: { tier: 'auto', key: 'cleanup' } }),
      finding({ name: 'version', status: 'info' })
    ]);
    expect(parts.problems.map((f) => f.name)).toEqual(['dns rule', 'tray']);
    expect(parts.optional.map((f) => f.name)).toEqual(['disk']);
    expect(parts.passed.map((f) => f.name)).toEqual(['agent']);
  });
});

describe('loadMachineReport', () => {
  it('loads the report, tolerates a null findings list and publishes it', async () => {
    mockFetch(JSON.stringify({ version: '1', findings: null, failures: 0, warnings: 0 }));
    const r = await loadMachineReport();
    expect(r.findings).toEqual([]);
    expect(get(machineReport)).toEqual(r);
  });

  it('surfaces an error the doctor answered with', async () => {
    mockFetch(JSON.stringify({ error: 'podman not found' }));
    await expect(loadMachineReport()).rejects.toThrow('podman not found');
  });
});

describe('applyMachineFix', () => {
  const agent = finding({ name: 'Windows agent running', status: 'warn', fix: { tier: 'auto', key: 'windows-agent' } });

  it('posts the fix by key and finding, streams its lines and resolves ok on done', async () => {
    const fn = mockFetch('{"phase":"output","line":"starting the lerd agent"}\n{"phase":"done"}\n');
    const lines: string[] = [];
    const r = await applyMachineFix(agent, (l) => lines.push(l));
    expect(r).toEqual({ ok: true });
    expect(lines).toEqual(['starting the lerd agent']);
    const [url, init] = fn.mock.calls[0];
    expect(url).toBe('/api/doctor/fix');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({ key: 'windows-agent', name: 'Windows agent running', message: '' });
  });

  it('reports the error a failed fix streams', async () => {
    mockFetch('{"phase":"failed","error":"the admin prompt was declined"}\n');
    const r = await applyMachineFix(agent, () => {});
    expect(r).toEqual({ ok: false, error: 'the admin prompt was declined' });
  });

  it('reports a refusal the server answers before streaming', async () => {
    mockFetch('that fix no longer applies, run the checks again\n', { status: 409 });
    const r = await applyMachineFix(agent, () => {});
    expect(r.ok).toBe(false);
    expect(r.error).toContain('no longer applies');
  });
});
