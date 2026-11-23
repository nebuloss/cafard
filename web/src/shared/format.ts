/** Times as people read them: "3 min ago", "expires in 5 h", "Yesterday 14:02". */
export class Time {
  constructor(private readonly now: () => number = Date.now) {}

  ago(iso: string | null | undefined): string {
    if (!iso) return 'never';
    const m = (this.now() - new Date(iso).getTime()) / 60000;
    if (m < 1) return 'just now';
    if (m < 60) return `${Math.floor(m)} min ago`;
    if (m < 1440) return `${Math.floor(m / 60)} h ago`;
    const d = Math.floor(m / 1440);
    return `${d} day${d === 1 ? '' : 's'} ago`;
  }

  until(iso: string): string {
    const m = (new Date(iso).getTime() - this.now()) / 60000;
    if (m <= 0) return 'expired';
    if (m < 60) return `expires in ${Math.ceil(m)} min`;
    // A day exactly reads as 24 h: links are made for 24 hours by default.
    if (m <= 1440) return `expires in ${Math.round(m / 60)} h`;
    const d = Math.round(m / 1440);
    return `expires in ${d} day${d === 1 ? '' : 's'}`;
  }

  /** Days since iso, whole. */
  daysSince(iso: string): number {
    return Math.floor((this.now() - new Date(iso).getTime()) / 864e5);
  }

  when(iso: string): string {
    const d = new Date(iso);
    const now = new Date(this.now());
    const hm = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
    if (d.toDateString() === now.toDateString()) return `Today ${hm}`;
    const y = new Date(now);
    y.setDate(now.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return `Yesterday ${hm}`;
    return `${d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} ${hm}`;
  }
}

/** Sizes in the units people expect: 4.3 GB, 312 MB. */
export class Bytes {
  static format(b: number): string {
    if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`;
    if (b >= 1 << 20) return `${Math.round(b / (1 << 20))} MB`;
    if (b >= 1024) return `${Math.round(b / 1024)} KB`;
    return `${b} B`;
  }
}

export const time = new Time();
