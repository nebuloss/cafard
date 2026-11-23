import { describe, expect, it } from 'vitest';
import { Html, html, escape } from '@shared/html';
import { Bytes, Time } from '@shared/format';
import { PickerState, type PickItem } from '@shared/picker';

describe('html', () => {
  it('escapes what it is given', () => {
    const name = '<img src=x onerror=alert(1)>';
    expect(html`<b>${name}</b>`.value).toBe('<b>&lt;img src=x onerror=alert(1)&gt;</b>');
    expect(html`<a title="${'"quoted"'}">`.value).toBe('<a title="&quot;quoted&quot;">');
  });

  it('keeps Html as it is, so components nest', () => {
    const inner = html`<i>${'a&b'}</i>`;
    expect(html`<p>${inner}</p>`.value).toBe('<p><i>a&amp;b</i></p>');
  });

  it('joins lists and drops nothing-values', () => {
    expect(html`${['a', html`<b>b</b>`, null, undefined, false]}`.value).toBe('a<b>b</b>');
    expect(html`${0}`.value).toBe('0');
    expect(Html.join([html`x`, html`y`], ',').value).toBe('x,y');
  });

  it('escape', () => {
    expect(escape(`&<>"'`)).toBe('&amp;&lt;&gt;&quot;&#39;');
    expect(escape(null)).toBe('');
  });
});

describe('Time', () => {
  const now = Date.parse('2026-11-23T12:00:00Z');
  const t = new Time(() => now);
  const minus = (min: number) => new Date(now - min * 60000).toISOString();

  it('ago', () => {
    expect(t.ago(null)).toBe('never');
    expect(t.ago(minus(0.5))).toBe('just now');
    expect(t.ago(minus(5))).toBe('5 min ago');
    expect(t.ago(minus(130))).toBe('2 h ago');
    expect(t.ago(minus(1440))).toBe('1 day ago');
    expect(t.ago(minus(1440 * 41))).toBe('41 days ago');
  });

  it('until', () => {
    expect(t.until(minus(1))).toBe('expired');
    expect(t.until(minus(-30))).toBe('expires in 30 min');
    expect(t.until(minus(-24 * 60))).toBe('expires in 24 h');
    expect(t.until(minus(-3 * 1440))).toBe('expires in 3 days');
  });
});

describe('Bytes', () => {
  it('format', () => {
    expect(Bytes.format(512)).toBe('512 B');
    expect(Bytes.format(4096)).toBe('4 KB');
    expect(Bytes.format(12 << 20)).toBe('12 MB');
    expect(Bytes.format(4.31e9)).toBe('4.0 GB');
  });
});

describe('PickerState', () => {
  const item = (id: string, kind = 'person', extra: Partial<PickItem> = {}): PickItem => ({
    id, label: id, kind, sub: '', avatar: Html.empty, text: id, ...extra,
  });
  const items = [item('zoe'), item('dev-code', 'agent'), item('dev-build', 'device'), item('alex'), item('lab-bench', 'agent', { disabled: true })];

  it('puts ticked items first, then by name, and keeps that order', () => {
    const s = new PickerState(items, ['zoe'], ['zoe']);
    expect(s.visible().map((i) => i.id)).toEqual(['zoe', 'alex', 'dev-build', 'dev-code', 'lab-bench']);
    s.toggle('alex');
    s.toggle('zoe');
    expect(s.visible().map((i) => i.id)).toEqual(['zoe', 'alex', 'dev-build', 'dev-code', 'lab-bench']);
  });

  it('searches and filters by kind', () => {
    const s = new PickerState(items, []);
    s.query = 'DEV';
    expect(s.visible().map((i) => i.id)).toEqual(['dev-build', 'dev-code']);
    s.kind = 'agent';
    expect(s.visible().map((i) => i.id)).toEqual(['dev-code']);
  });

  it('says what saving changes', () => {
    const s = new PickerState(items, ['zoe', 'dev-code'], ['zoe', 'dev-code']);
    expect(s.saveLabel()).toBe('Save');
    s.toggle('dev-code');
    s.toggle('alex');
    s.toggle('dev-build');
    expect(s.changes()).toEqual({ add: ['alex', 'dev-build'], remove: ['dev-code'] });
    expect(s.saveLabel()).toBe('Save · +2 −1');
    expect(s.tag('alex')).toBe('add');
    expect(s.tag('dev-code')).toBe('remove');
    expect(s.tag('zoe')).toBe('member');
    expect(s.tag('lab-bench')).toBeNull();
  });

  it('cannot tick a disabled item, and Enter ticks the first that can be', () => {
    const s = new PickerState(items, []);
    s.toggle('lab-bench');
    expect(s.selected.has('lab-bench')).toBe(false);
    s.query = 'lab';
    expect(s.first()).toBeUndefined();
    s.query = 'dev';
    expect(s.first()?.id).toBe('dev-build');
  });
});
