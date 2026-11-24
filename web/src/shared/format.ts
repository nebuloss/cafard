import { i18n, n, t } from './i18n';

/** Times as people read them: "3 min ago", "expires in 5 h", "Yesterday 14:02". */
export class Time {
  constructor(private readonly now: () => number = Date.now) {}

  ago(iso: string | null | undefined): string {
    if (!iso) return t('never');
    const m = (this.now() - new Date(iso).getTime()) / 60000;
    if (m < 1) return t('just now');
    if (m < 60) return t('{n} min ago', { n: Math.floor(m) });
    if (m < 1440) return t('{n} h ago', { n: Math.floor(m / 60) });
    return n(Math.floor(m / 1440), '{n} day ago', '{n} days ago');
  }

  until(iso: string): string {
    const m = (new Date(iso).getTime() - this.now()) / 60000;
    if (m <= 0) return t('expired');
    if (m < 60) return t('expires in {n} min', { n: Math.ceil(m) });
    // A day exactly reads as 24 h: links are made for 24 hours by default.
    if (m <= 1440) return t('expires in {n} h', { n: Math.round(m / 60) });
    return n(Math.round(m / 1440), 'expires in {n} day', 'expires in {n} days');
  }

  /** Days since iso, whole. */
  daysSince(iso: string): number {
    return Math.floor((this.now() - new Date(iso).getTime()) / 864e5);
  }

  when(iso: string): string {
    const d = new Date(iso);
    const now = new Date(this.now());
    const hm = d.toLocaleTimeString(i18n.locale, { hour: '2-digit', minute: '2-digit' });
    if (d.toDateString() === now.toDateString()) return t('Today {time}', { time: hm });
    const y = new Date(now);
    y.setDate(now.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return t('Yesterday {time}', { time: hm });
    return `${d.toLocaleDateString(i18n.locale, { day: 'numeric', month: 'short' })} ${hm}`;
  }
}

/** Sizes in the units people expect: 4.3 GB, 312 MB; French says octets. */
export class Bytes {
  static format(b: number): string {
    const num = (v: number, digits = 0) => v.toLocaleString(i18n.locale, { maximumFractionDigits: digits, minimumFractionDigits: digits });
    if (b >= 1 << 30) return t('{n} GB', { n: num(b / (1 << 30), 1) });
    if (b >= 1 << 20) return t('{n} MB', { n: num(Math.round(b / (1 << 20))) });
    if (b >= 1024) return t('{n} KB', { n: num(Math.round(b / 1024)) });
    return t('{n} B', { n: b });
  }
}

export const time = new Time();
