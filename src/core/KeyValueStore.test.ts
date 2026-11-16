import { describe, expect, it } from 'vitest';
import { isNumber, isString, LocalStorageStore, MemoryStore, StorageKey } from '@/core/KeyValueStore.ts';

const lastChat = new StorageKey('cafard.lastChat', isString);
const width = new StorageKey('cafard.logsWidth', isNumber);

describe.each([
  ['MemoryStore', () => new MemoryStore()],
  ['LocalStorageStore', () => new LocalStorageStore(globalThis.localStorage)],
])('%s', (_name, make) => {
  it('round-trips a value', () => {
    const store = make();
    expect(store.get(lastChat)).toBeNull();
    store.set(lastChat, 'chan:dut1');
    expect(store.get(lastChat)).toBe('chan:dut1');
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
