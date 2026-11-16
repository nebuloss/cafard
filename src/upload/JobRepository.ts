import type { JobData } from '@/upload/UploadJob.ts';

/** Where pending jobs wait while offline or across a reload. */
export interface JobRepository {
  all(): Promise<JobData[]>;
  put(job: JobData): Promise<void>;
  delete(id: string): Promise<void>;
}

/** For tests and browsers without IndexedDB: jobs live as long as the page. */
export class MemoryJobRepository implements JobRepository {
  private readonly jobs = new Map<string, JobData>();

  all(): Promise<JobData[]> {
    return Promise.resolve([...this.jobs.values()].sort((a, b) => a.createdAt - b.createdAt));
  }

  put(job: JobData): Promise<void> {
    this.jobs.set(job.id, job);
    return Promise.resolve();
  }

  delete(id: string): Promise<void> {
    this.jobs.delete(id);
    return Promise.resolve();
  }
}

const DB = 'cafard';
const STORE = 'uploads';

/**
 * IndexedDB: the files are stored as Blobs with the job, so a message
 * written offline is sent when the network comes back, even after the
 * tab was closed.
 */
export class IndexedDbJobRepository implements JobRepository {
  private db: Promise<IDBDatabase> | null = null;

  constructor(private readonly factory: IDBFactory) {}

  private open(): Promise<IDBDatabase> {
    this.db ??= new Promise((resolve, reject) => {
      const req = this.factory.open(DB, 1);
      req.onupgradeneeded = () => {
        if (!req.result.objectStoreNames.contains(STORE)) req.result.createObjectStore(STORE, { keyPath: 'id' });
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error ?? new Error('IndexedDB refused'));
    });
    return this.db;
  }

  private async run<T>(mode: IDBTransactionMode, fn: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
    const db = await this.open();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, mode);
      const req = fn(tx.objectStore(STORE));
      tx.oncomplete = () => resolve(req.result);
      tx.onerror = () => reject(tx.error ?? new Error('IndexedDB failed'));
    });
  }

  async all(): Promise<JobData[]> {
    const jobs = (await this.run('readonly', (s) => s.getAll())) as JobData[];
    return jobs.sort((a, b) => a.createdAt - b.createdAt);
  }

  async put(job: JobData): Promise<void> {
    await this.run('readwrite', (s) => s.put(job));
  }

  async delete(id: string): Promise<void> {
    await this.run('readwrite', (s) => s.delete(id));
  }
}

/** IndexedDB when the browser has it, memory otherwise. */
export function jobRepository(win: { indexedDB?: IDBFactory } | undefined): JobRepository {
  try {
    if (win?.indexedDB !== undefined) return new IndexedDbJobRepository(win.indexedDB);
  } catch {
    // Private modes may throw on access.
  }
  return new MemoryJobRepository();
}
