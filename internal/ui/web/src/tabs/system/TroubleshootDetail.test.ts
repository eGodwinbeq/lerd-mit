import { render, screen, cleanup, fireEvent } from '@testing-library/svelte';
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import type { Writable } from 'svelte/store';

const loadMachineReport = vi.fn();
const applyMachineFix = vi.fn();

vi.mock('$stores/troubleshoot', async (orig) => {
  const real = await orig<typeof import('$stores/troubleshoot')>();
  return {
    ...real,
    loadMachineReport: (...a: unknown[]) => loadMachineReport(...a),
    applyMachineFix: (...a: unknown[]) => applyMachineFix(...a)
  };
});
vi.mock('$stores/accessMode', async () => {
  const { writable } = await import('svelte/store');
  return { accessMode: writable({ localControl: true }) };
});

import { accessMode as accessModeStore } from '$stores/accessMode';
import TroubleshootDetail from './TroubleshootDetail.svelte';

const accessMode = accessModeStore as unknown as Writable<{ localControl: boolean }>;

const report = {
  version: 'test',
  failures: 1,
  warnings: 2,
  findings: [
    { section: 'WSL2', name: '.test routed to lerd in Windows', status: 'fail', message: 'no DNS rule', fix: { tier: 'auto', key: 'windows-dns', label: 'add the Windows DNS rule for .test', confirm: 'Windows asks for administrator rights.' } },
    { section: 'WSL2', name: 'Windows agent running', status: 'warn', message: 'nothing boots WSL', fix: { tier: 'auto', key: 'windows-agent', label: 'start the lerd agent' } },
    { section: 'WSL2', name: 'Windows integration', status: 'warn', message: 'lerd.exe is not set up', fix: { tier: 'manual', label: 'run `lerd wsl:setup`' } },
    { section: 'DNS', name: 'resolver', status: 'ok' }
  ]
};

beforeEach(() => {
  accessMode.set({ localControl: true });
  loadMachineReport.mockResolvedValue(report);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('TroubleshootDetail', () => {
  it('runs the checks on open and lists problems with a Fix or the command to run', async () => {
    render(TroubleshootDetail);
    expect(await screen.findByText('Windows agent running')).toBeTruthy();
    expect(loadMachineReport).toHaveBeenCalledTimes(1);
    expect(screen.getByText('nothing boots WSL')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: 'Fix' })).toHaveLength(2);
    expect(screen.getByText('run `lerd wsl:setup`')).toBeTruthy();
    expect(screen.getByText('1 checks passed')).toBeTruthy();
  });

  it('applies a light fix straight away, shows its output and re-checks', async () => {
    applyMachineFix.mockImplementation(async (_f: unknown, onLine: (l: string) => void) => {
      onLine('starting the lerd agent');
      return { ok: true };
    });
    render(TroubleshootDetail);
    await screen.findByText('Windows agent running');
    const agentRow = screen.getByText('Windows agent running').closest('[data-finding]') as HTMLElement;
    await fireEvent.click(agentRow.querySelector('button') as HTMLButtonElement);
    expect(applyMachineFix).toHaveBeenCalledWith(expect.objectContaining({ name: 'Windows agent running' }), expect.any(Function));
    expect(await screen.findByText('starting the lerd agent')).toBeTruthy();
    await vi.waitFor(() => expect(loadMachineReport).toHaveBeenCalledTimes(2));
  });

  it('asks before a heavy fix and runs nothing when cancelled', async () => {
    render(TroubleshootDetail);
    await screen.findByText('.test routed to lerd in Windows');
    const row = screen.getByText('.test routed to lerd in Windows').closest('[data-finding]') as HTMLElement;
    await fireEvent.click(row.querySelector('button') as HTMLButtonElement);
    expect(await screen.findByText('Apply this fix?')).toBeTruthy();
    expect(screen.getByText(/Windows asks for administrator rights/)).toBeTruthy();
    expect(applyMachineFix).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(applyMachineFix).not.toHaveBeenCalled();
  });

  it('hides Fix buttons from a session without local control', async () => {
    accessMode.set({ localControl: false });
    render(TroubleshootDetail);
    await screen.findByText('Windows agent running');
    expect(screen.queryByRole('button', { name: 'Fix' })).toBeNull();
  });

  it('shows the error when the checks cannot run', async () => {
    loadMachineReport.mockRejectedValue(new Error('podman not found'));
    render(TroubleshootDetail);
    expect(await screen.findByText('podman not found')).toBeTruthy();
  });
});

describe('TroubleshootDetail edge cases', () => {
  it('renders findings whose names were cut short to the same text', async () => {
    const dup = { section: 'Configuration', name: 'parked dir: /home/user/projects/cli...', status: 'warn', fix: { tier: 'auto', key: 'mkdir', label: 'create it' } };
    loadMachineReport.mockResolvedValue({
      version: 'test',
      failures: 0,
      warnings: 2,
      findings: [
        { ...dup, message: 'missing clients-a' },
        { ...dup, message: 'missing clients-b' }
      ]
    });
    render(TroubleshootDetail);
    expect(await screen.findByText('missing clients-a')).toBeTruthy();
    expect(screen.getByText('missing clients-b')).toBeTruthy();
  });

  it('shows a fix that failed to reach the server and checks again', async () => {
    applyMachineFix.mockRejectedValue(new Error('Failed to fetch'));
    render(TroubleshootDetail);
    await screen.findByText('Windows agent running');
    const row = screen.getByText('Windows agent running').closest('[data-finding]') as HTMLElement;
    await fireEvent.click(row.querySelector('button') as HTMLButtonElement);
    expect(await screen.findByText('Failed to fetch')).toBeTruthy();
    await vi.waitFor(() => expect(loadMachineReport).toHaveBeenCalledTimes(2));
  });
});
