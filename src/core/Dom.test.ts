import { describe, expect, it } from 'vitest';
import { Dom, h, isSafeUrl } from '@/core/Dom.ts';

describe('h', () => {
  it('puts strings in as text, never as markup', () => {
    const el = h('p', { class: 'line' }, '<img src=x onerror=alert(1)>', h('b', {}, 'bold'));
    expect(el.textContent).toBe('<img src=x onerror=alert(1)>bold');
    expect(el.querySelector('img')).toBeNull();
    expect(el.querySelector('b')?.textContent).toBe('bold');
    expect(el.className).toBe('line');
  });

  it('handles boolean and missing attributes', () => {
    const el = h('button', { disabled: true, hidden: false, title: null });
    expect(el.hasAttribute('disabled')).toBe(true);
    expect(el.hasAttribute('hidden')).toBe(false);
    expect(el.hasAttribute('title')).toBe(false);
  });

  it('refuses event handlers and inline style', () => {
    expect(() => h('div', { onclick: 'x()' })).toThrow();
    expect(() => h('div', { OnLoad: 'x()' })).toThrow();
    expect(() => h('div', { style: 'color:red' })).toThrow();
  });

  it('refuses script URLs', () => {
    expect(() => h('a', { href: 'javascript:alert(1)' })).toThrow();
    expect(() => h('img', { src: 'data:image/png;base64,AA' })).toThrow();
    expect(h('a', { href: '/c/lab/dut1' }).getAttribute('href')).toBe('/c/lab/dut1');
  });
});

describe('isSafeUrl', () => {
  it.each([
    ['/blob/abc', true],
    ['#m12', true],
    ['?f=all', true],
    ['https://example.com/x', true],
    ['HTTP://example.com', true],
    ['//evil.example', false],
    ['/\\evil.example', false],
    ['javascript:alert(1)', false],
    [' javascript:alert(1)', false],
    ['data:text/html,x', false],
    ['vbscript:x', false],
  ])('%s -> %s', (url, safe) => {
    expect(isSafeUrl(url)).toBe(safe);
  });
});

describe('Dom', () => {
  it('finds typed elements and reads numbers', () => {
    document.body.replaceChildren(h('div', { 'data-seq': '42', id: 'a' }), h('div', { 'data-seq': 'x' }));
    const first = Dom.find('#a');
    expect(first).not.toBeNull();
    expect(Dom.number(first!, 'data-seq')).toBe(42);
    expect(Dom.findAll('[data-seq]')).toHaveLength(2);
    expect(Dom.number(Dom.findAll('[data-seq]')[1]!, 'data-seq')).toBeNull();
    expect(Dom.find('#missing')).toBeNull();
  });
});
