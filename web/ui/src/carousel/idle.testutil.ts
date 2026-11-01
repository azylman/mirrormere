import { act } from '@testing-library/react';
import { vi } from 'vitest';

export interface IdleStub {
  /** Timeouts passed to requestIdleCallback, one per call. */
  timeouts: Array<number | undefined>;
  /** Callbacks waiting for idle time. */
  pending(): number;
  /** Runs every waiting callback inside act, so the fill transition commits. */
  flush(): Promise<void>;
}

/** Installs a manual requestIdleCallback (jsdom has none). unstubAllGlobals removes it. */
export function stubIdle(): IdleStub {
  const queue = new Map<number, () => void>();
  const timeouts: Array<number | undefined> = [];
  let next = 1;
  vi.stubGlobal('requestIdleCallback', (cb: () => void, options?: { timeout?: number }) => {
    timeouts.push(options?.timeout);
    const id = next++;
    queue.set(id, cb);
    return id;
  });
  vi.stubGlobal('cancelIdleCallback', (id: number) => {
    queue.delete(id);
  });
  return {
    timeouts,
    pending: () => queue.size,
    async flush() {
      const callbacks = [...queue.values()];
      queue.clear();
      await act(async () => {
        for (const cb of callbacks) cb();
      });
    },
  };
}

/**
 * Removes the stub completely. vi.unstubAllGlobals leaves the property behind as undefined,
 * and fake timers then install their own requestIdleCallback over it.
 */
export function unstubIdle(): void {
  vi.unstubAllGlobals();
  Reflect.deleteProperty(globalThis, 'requestIdleCallback');
  Reflect.deleteProperty(globalThis, 'cancelIdleCallback');
}
