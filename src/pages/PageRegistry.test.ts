import { describe, expect, it, vi } from 'vitest';
import { App } from '@/App.ts';
import { disposable } from '@/core/Disposable.ts';
import { MemoryStore } from '@/core/KeyValueStore.ts';
import { PageRegistry } from '@/pages/PageRegistry.ts';
import { FakeClock } from '@/testing/FakeClock.ts';

describe('PageRegistry', () => {
  it('picks the page from body data-page', () => {
    document.body.dataset['page'] = 'home';
    const start = vi.fn(() => disposable(() => {}));
    const registry = new PageRegistry().register('home', () => ({ start }));
    registry.create(document)?.start();
    expect(start).toHaveBeenCalled();
  });

  it('returns null for an unknown or missing page', () => {
    const registry = new PageRegistry();
    document.body.dataset['page'] = 'nope';
    expect(registry.create(document)).toBeNull();
    delete document.body.dataset['page'];
    expect(registry.create(document)).toBeNull();
  });

  it('refuses a duplicate registration', () => {
    const registry = new PageRegistry().register('home', () => ({ start: () => disposable(() => {}) }));
    expect(() => registry.register('home', () => ({ start: () => disposable(() => {}) }))).toThrow();
  });
});

describe('App', () => {
  it('starts the registered page and disposes it on stop', () => {
    document.body.dataset['page'] = 'test-page';
    const stopped = vi.fn();
    const app = new App(document, { clock: new FakeClock(), storage: new MemoryStore() });
    app.pages.register('test-page', () => ({ start: () => disposable(stopped) }));
    const running = app.start();
    expect(document.documentElement.classList.contains('has-script')).toBe(true);
    running.dispose();
    expect(stopped).toHaveBeenCalledTimes(1);
  });
});
