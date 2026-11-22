import { vi } from 'vitest';

type ResizeCallback = (entries: { contentRect: { width: number; height: number } }[]) => void;

/** Handle returned by stubResizeObserver. observe/disconnect/unobserve are shared by every instance. */
export interface ResizeObserverStub {
  /** Delivers a content box of `width` x `height` to the most recently constructed observer. */
  resize(width: number, height: number): void;
  observe: ReturnType<typeof vi.fn>;
  disconnect: ReturnType<typeof vi.fn>;
  unobserve: ReturnType<typeof vi.fn>;
}

/**
 * Replaces the global ResizeObserver for the current test (unstubGlobals restores it).
 * resize() throws when the code under test never constructed an observer.
 */
export function stubResizeObserver(): ResizeObserverStub {
  let latest: ResizeCallback | null = null;
  const observe = vi.fn();
  const disconnect = vi.fn();
  const unobserve = vi.fn();
  class StubResizeObserver {
    observe = observe;
    disconnect = disconnect;
    unobserve = unobserve;
    constructor(callback: ResizeCallback) {
      latest = callback;
    }
  }
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  return {
    resize(width, height) {
      if (!latest) throw new Error('stubResizeObserver: no ResizeObserver was constructed');
      latest([{ contentRect: { width, height } }]);
    },
    observe,
    disconnect,
    unobserve,
  };
}
