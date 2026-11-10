// Visual treatment for a doctor result, shared by the site doctor and the
// machine-wide troubleshooting panel: the result icon, its colour, the count
// pill styling and the severity order problems are listed in.
export type DoctorStatus = 'ok' | 'warn' | 'fail' | 'info' | 'unknown';

export interface DoctorStatusStyle {
  rank: number;
  icon: string;
  text: string;
  badge: string;
  dot: string;
}

export const DOCTOR_STATUS: Record<DoctorStatus, DoctorStatusStyle> = {
  fail: {
    rank: 0,
    icon: 'M9 9l6 6m0-6l-6 6m12-3a9 9 0 11-18 0 9 9 0 0118 0z',
    text: 'text-red-500',
    badge: 'bg-red-500/15 text-red-400',
    dot: 'bg-red-500'
  },
  warn: {
    rank: 1,
    icon: 'M12 9v2m0 4h.01M5.07 19h13.86a2 2 0 001.74-3L13.74 4a2 2 0 00-3.48 0L3.33 16a2 2 0 001.74 3z',
    text: 'text-amber-500',
    badge: 'bg-amber-500/15 text-amber-500',
    dot: 'bg-amber-500'
  },
  info: {
    rank: 2,
    icon: 'M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
    text: 'text-sky-500',
    badge: 'bg-sky-500/15 text-sky-500',
    dot: 'bg-sky-500'
  },
  unknown: {
    rank: 3,
    icon: 'M8.228 9c.549-1.165 2.03-2 3.772-2 2.21 0 4 1.343 4 3 0 1.4-1.278 2.575-3.006 2.907-.542.104-.994.54-.994 1.093m0 3h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
    text: 'text-gray-400',
    badge: 'bg-gray-500/15 text-gray-400',
    dot: 'bg-gray-400'
  },
  ok: {
    rank: 4,
    icon: 'M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z',
    text: 'text-emerald-500',
    badge: 'bg-emerald-500/15 text-emerald-500',
    dot: 'bg-emerald-500'
  }
};

export function doctorStatus(s: string): DoctorStatusStyle {
  return DOCTOR_STATUS[(s as DoctorStatus) in DOCTOR_STATUS ? (s as DoctorStatus) : 'unknown'];
}
