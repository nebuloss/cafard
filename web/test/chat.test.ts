import { describe, expect, it } from 'vitest';
import { MessageText } from '../src/chat/text';
import { Address } from '../src/chat/store';

describe('MessageText', () => {
  const t = new MessageText(new Set(['dev-code', 'lab-bench']));

  it('escapes first, so nothing written becomes markup', () => {
    expect(t.render('<img src=x onerror=alert(1)>').value).toBe('&lt;img src=x onerror=alert(1)&gt;');
    expect(t.render('`<b>`').value).toBe('<code>&lt;b&gt;</code>');
  });

  it('marks @members it knows, and only those', () => {
    expect(t.render('@dev-code can you look? cc @nobody').value).toBe('<span class="mention">@dev-code</span> can you look? cc @nobody');
    expect(t.render('mail me@dev-code').value).toBe('mail me@dev-code');
  });

  it('marks code and links', () => {
    expect(t.render('found `dhd_bus_rxctl` at line 1842').value).toBe('found <code>dhd_bus_rxctl</code> at line 1842');
    expect(t.render('see https://example.com/a?b=1.').value).toBe('see <a href="https://example.com/a?b=1" target="_blank" rel="noopener noreferrer">https://example.com/a?b=1</a>.');
  });

  it('finds the @name being typed', () => {
    expect(MessageText.mentionAt('hello @dev')).toBe('dev');
    expect(MessageText.mentionAt('@')).toBe('');
    expect(MessageText.mentionAt('mail me@dev')).toBeNull();
    expect(MessageText.mentionAt('hello')).toBeNull();
  });
});

describe('Address', () => {
  it('parses and encodes', () => {
    const a = Address.parse('be98/wifi-drops');
    expect(a?.project).toBe('be98');
    expect(a?.name).toBe('wifi-drops');
    expect(a?.path).toBe('be98/wifi-drops');
    expect(Address.parse('be98')).toBeNull();
    expect(Address.parse('be98/')).toBeNull();
  });
});
