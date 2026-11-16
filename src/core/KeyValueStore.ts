/** A key and the check that turns a stored value back into its type. */
export class StorageKey<T> {
  constructor(
    readonly name: string,
    readonly parse: (raw: unknown) => T | null,
  ) {}
}

export const isString = (raw: unknown): string | null => (typeof raw === 'string' ? raw : null);
export const isNumber = (raw: unknown): number | null => (typeof raw === 'number' && Number.isFinite(raw) ? raw : null);

/** Small persistent settings. Values are JSON; reads are validated. */
export interface KeyValueStore {
  get<T>(key: StorageKey<T>): T | null;
  set<T>(key: StorageKey<T>, value: T): void;
}

/**
 * localStorage, tolerant of a browser that refuses it (private mode, quota,
 * disabled storage): a failed write is lost, a failed read is null.
 */
export class LocalStorageStore implements KeyValueStore {
  constructor(private readonly storage: Storage | null = LocalStorageStore.available()) {}

  private static available(): Storage | null {
    try {
      return globalThis.localStorage ?? null;
    } catch {
      return null;
    }
  }

  get<T>(key: StorageKey<T>): T | null {
    try {
      const raw = this.storage?.getItem(key.name);
      if (raw === null || raw === undefined) return null;
      return key.parse(JSON.parse(raw));
    } catch {
      return null;
    }
  }

  set<T>(key: StorageKey<T>, value: T): void {
    try {
      this.storage?.setItem(key.name, JSON.stringify(value));
    } catch {
      // Storage full or refused: the setting is a convenience, not data.
    }
  }
}

/** In memory, for tests and for browsers with no storage at all. */
export class MemoryStore implements KeyValueStore {
  private readonly values = new Map<string, string>();

  get<T>(key: StorageKey<T>): T | null {
    const raw = this.values.get(key.name);
    return raw === undefined ? null : key.parse(JSON.parse(raw));
  }

  set<T>(key: StorageKey<T>, value: T): void {
    this.values.set(key.name, JSON.stringify(value));
  }
}
