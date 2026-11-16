import { describe, expect, it } from 'vitest';
import { isNumber, isString, LocalStorageStore, MemoryStore, StorageKey } from '@/core/KeyValueStore.ts';

const lastChat = new StorageKey('cafard.lastChat', isString);
const width = new StorageKey('cafard.logsWidth', isNumber);

describe.each([
  ['MemoryStore', () => new MemoryStore()],
  ['LocalStorageStore', () => new LocalStorageStore(globalThis.localStorage)],
])('%s', (_name, make) => {
  it('round-trips a value and removes it', () => {
    const store = make();
    store.set(lastChat, 'chan:dut1');
    expect(store.get(lastChat)).toBe('chan:dut1');
    store.remove(lastChat);
    expect(store.get(lastChat)).toBeNull();
  });

  it('returns null for a value of the wrong type', () => {
    const store = make();
    store.set(new StorageKey('cafard.logsWidth', isString), 'wide');
    expect(store.get(width)).toBeNull();
  });
});

describe('LocalStorageStore', () => {
  it('survives a browser with no storage', () => {
    const store = new LocalStorageStore(null);
    store.set(lastChat, 'x');
    expect(store.get(lastChat)).toBeNull();
  });

  it('returns null for corrupt JSON', () => {
    globalThis.localStorage.setItem('cafard.lastChat', '{not json');
    expect(new LocalStorageStore(globalThis.localStorage).get(lastChat)).toBeNull();
  });
});
