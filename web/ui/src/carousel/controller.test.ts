import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../store';
import { CarouselController, neighbourOf, type CarouselHost } from './controller';
import { SETTLE_FALLBACK_MS, SETTLE_MS } from './track';

const initial = useDisplayStore.getState();

interface Harness {
  viewport: HTMLDivElement;
  track: HTMLDivElement;
  host: { commit: ReturnType<typeof vi.fn>; select: ReturnType<typeof vi.fn> } & CarouselHost;
  controller: CarouselController;
  clock: { t: number };
  frames: Array<() => void>;
  flushFrames(): void;
  down(x: number, y: number, target?: EventTarget): void;
  move(x: number, y: number, ms?: number): void;
  up(x: number, y: number, ms?: number): void;
  cancel(): void;
  transitionEnd(): void;
}

let harness: Harness | null = null;

function pointer(target: EventTarget, type: string, x: number, y: number): void {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.assign(event, { clientX: x, clientY: y, pointerId: 1, isPrimary: true });
  target.dispatchEvent(event);
}

function build(initialIndex = 1, width = 1000): Harness {
  const viewport = document.createElement('div');
  const track = document.createElement('div');
  viewport.appendChild(track);
  document.body.appendChild(viewport);
  Object.defineProperty(viewport, 'clientWidth', { value: width, configurable: true });
  Object.defineProperty(track, 'clientWidth', { value: width, configurable: true });

  const frames: Array<() => void> = [];
  vi.stubGlobal('requestAnimationFrame', (cb: () => void) => {
    frames.push(cb);
    return frames.length;
  });
  vi.stubGlobal('cancelAnimationFrame', () => {});

  const host = { commit: vi.fn(), select: vi.fn() };
  const clock = { t: 0 };
  const controller = new CarouselController({
    viewport,
    track,
    host,
    initial: initialIndex,
    now: () => clock.t,
  });
  controller.attach();

  const h: Harness = {
    viewport,
    track,
    host,
    controller,
    clock,
    frames,
    flushFrames() {
      while (frames.length > 0) frames.shift()?.();
    },
    down(x, y, target = viewport) {
      pointer(target, 'pointerdown', x, y);
    },
    move(x, y, ms = 16) {
      clock.t += ms;
      pointer(viewport, 'pointermove', x, y);
    },
    up(x, y, ms = 16) {
      clock.t += ms;
      pointer(viewport, 'pointerup', x, y);
    },
    cancel() {
      pointer(viewport, 'pointercancel', 0, 0);
    },
    transitionEnd() {
      const event = new Event('transitionend');
      Object.assign(event, { propertyName: 'transform' });
      track.dispatchEvent(event);
    },
  };
  harness = h;
  return h;
}

/**
 * Horizontal swipe from x0 to x1 in four 200 ms steps. The final velocity is the last
 * step's pace, 0.375 px/ms for a 300 px drag: under the flick threshold, so only
 * distance decides.
 */
function swipe(h: Harness, x0: number, x1: number): void {
  h.down(x0, 100);
  const steps = 4;
  for (let i = 1; i <= steps; i++) {
    h.move(x0 + ((x1 - x0) * i) / steps, 100, 200);
  }
  h.flushFrames();
  h.up(x1, 100, 0);
}

describe('neighbourOf', () => {
  it('wraps at both ends, as the server advance does', () => {
    expect(neighbourOf(0, 3, 'prev')).toBe(2);
    expect(neighbourOf(2, 3, 'next')).toBe(0);
    expect(neighbourOf(1, 3, 'next')).toBe(2);
  });
});

describe('CarouselController', () => {
  beforeEach(() => {
    useDisplayStore.setState(initial, true);
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 1, totalScreens: 3 } });
  });

  afterEach(() => {
    harness?.controller.dispose();
    harness?.viewport.remove();
    harness = null;
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('commits a left drag past 25% after the settle, and sends the select only then', () => {
    vi.useFakeTimers();
    const h = build();
    swipe(h, 500, 200);
    h.flushFrames();

    expect(h.track.style.transform).toBe('translate3d(-1000px,0,0)');
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.host.select).not.toHaveBeenCalled();
    expect(document.body.dataset.animating).toBe('1');

    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(2);
    expect(h.host.select).toHaveBeenCalledWith(2);
    expect(document.body.dataset.animating).toBe('0');
  });

  it('waits for transitionend rather than the nominal 250 ms, then settles on the fallback timer', () => {
    vi.useFakeTimers();
    const h = build();
    swipe(h, 500, 200);
    vi.advanceTimersByTime(SETTLE_MS + 100);
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.host.select).not.toHaveBeenCalled();
    vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
    expect(h.host.commit).toHaveBeenCalledWith(2);
    expect(h.host.select).toHaveBeenCalledWith(2);
  });

  it('springs back below 25% without committing or selecting', () => {
    vi.useFakeTimers();
    const h = build();
    swipe(h, 500, 400);
    h.flushFrames();
    expect(h.track.style.transform).toBe('translate3d(0px,0,0)');

    h.transitionEnd();
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.host.select).not.toHaveBeenCalled();
    expect(h.track.style.transform).toBe('');
    expect(document.body.dataset.animating).toBe('0');
  });

  it('commits a short flick above 0.4 px/ms', () => {
    vi.useFakeTimers();
    const h = build();
    h.down(500, 100);
    h.move(460, 100);
    h.flushFrames();
    // 60 px over 16 ms is 3.75 px/ms: well past the velocity threshold.
    h.up(440, 100);
    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(2);
  });

  it('commits a right drag to the previous screen, wrapping from the first', () => {
    vi.useFakeTimers();
    const h = build(0);
    swipe(h, 300, 700);
    h.flushFrames();
    expect(h.track.style.transform).toBe('translate3d(1000px,0,0)');
    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(2);
    expect(h.host.select).toHaveBeenCalledWith(2);
  });

  it('treats a tap as nothing', () => {
    const h = build();
    h.down(500, 100);
    h.up(502, 101);
    expect(h.track.style.transform).toBe('');
    expect(h.host.commit).not.toHaveBeenCalled();
  });

  it('releases a vertical intent to the page without moving the track', () => {
    vi.useFakeTimers();
    const h = build();
    h.down(500, 100);
    h.move(505, 140);
    h.move(300, 150);
    h.flushFrames();
    h.up(300, 150);
    expect(h.frames).toHaveLength(0);
    expect(h.track.style.transform).toBe('');
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.host.select).not.toHaveBeenCalled();
  });

  it('springs back when the browser cancels a horizontal drag', () => {
    vi.useFakeTimers();
    const h = build();
    h.down(500, 100);
    h.move(450, 100);
    h.flushFrames();
    h.cancel();
    expect(h.track.style.transform).toBe('translate3d(0px,0,0)');
    h.transitionEnd();
    expect(h.host.commit).not.toHaveBeenCalled();
  });

  it('ignores a swipe that starts inside an excluded zone', () => {
    vi.useFakeTimers();
    const h = build();
    const grid = document.createElement('div');
    grid.className = 'widget-calendar-grid';
    const cell = document.createElement('span');
    grid.appendChild(cell);
    h.viewport.appendChild(grid);

    h.down(500, 100, cell);
    h.move(300, 100);
    h.flushFrames();
    h.up(300, 100);
    expect(h.frames).toHaveLength(0);
    expect(h.track.style.transform).toBe('');
    expect(h.host.commit).not.toHaveBeenCalled();
  });

  it('ignores a swipe when the carousel is paused', () => {
    vi.useFakeTimers();
    const h = build();
    useDisplayStore.getState().pause();
    swipe(h, 500, 200);
    h.flushFrames();
    expect(h.track.style.transform).toBe('');
    expect(h.host.commit).not.toHaveBeenCalled();
  });

  it('ignores swipes when there is only one screen', () => {
    vi.useFakeTimers();
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 0, totalScreens: 1 } });
    const h = build(0);
    swipe(h, 500, 100);
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.track.style.transform).toBe('');
  });

  it('does nothing when the echo of our own select names the screen already shown', () => {
    vi.useFakeTimers();
    const h = build();
    h.controller.requestShow(1);
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.track.style.transform).toBe('');
    expect(h.frames).toHaveLength(0);
  });

  it('settles a server rotate to an adjacent screen without a select', () => {
    vi.useFakeTimers();
    const h = build();
    h.controller.requestShow(2);
    expect(h.track.style.transform).toBe('translate3d(-1000px,0,0)');
    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(2);
    expect(h.host.select).not.toHaveBeenCalled();
  });

  it('jumps without animation to a screen that is not a neighbour', () => {
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 0, totalScreens: 4 } });
    const h = build(0);
    h.controller.requestShow(2);
    expect(h.host.commit).toHaveBeenCalledWith(2);
    expect(h.host.select).not.toHaveBeenCalled();
    expect(h.track.style.transform).toBe('');
  });

  it('queues a server rotate that arrives mid-drag and runs it after the release', () => {
    vi.useFakeTimers();
    const h = build();
    h.down(500, 100);
    h.move(480, 100);
    h.flushFrames();
    h.controller.requestShow(2);
    expect(h.host.commit).not.toHaveBeenCalled();

    // Short drag: springs back, then the queued rotate slides to screen 2.
    h.move(490, 100);
    h.up(490, 100);
    h.transitionEnd();
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.track.style.transform).toBe('translate3d(-1000px,0,0)');

    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(2);
    expect(h.host.select).not.toHaveBeenCalled();
  });

  it('queues a server rotate that arrives during a settle until React has rendered the commit', () => {
    vi.useFakeTimers();
    const h = build();
    swipe(h, 500, 200);
    h.controller.requestShow(0);
    expect(h.host.commit).not.toHaveBeenCalled();

    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(2);
    expect(h.host.commit).toHaveBeenCalledTimes(1);

    // React renders screen 2: the queued rotate to 0 (a wrap, so 'next') starts now.
    h.controller.rendered(2);
    expect(h.track.style.transform).toBe('translate3d(-1000px,0,0)');
    h.transitionEnd();
    expect(h.host.commit).toHaveBeenLastCalledWith(0);
    expect(h.host.commit).toHaveBeenCalledTimes(2);
    expect(h.host.select).toHaveBeenCalledTimes(1);
  });

  it('applies a dot or pill selection with the same slide and a select after the settle', () => {
    vi.useFakeTimers();
    const h = build();
    h.controller.goTo(0);
    expect(h.track.style.transform).toBe('translate3d(1000px,0,0)');
    expect(h.host.select).not.toHaveBeenCalled();
    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(0);
    expect(h.host.select).toHaveBeenCalledWith(0);
  });

  it('ignores dot selection while paused or already on that screen', () => {
    const h = build();
    h.controller.goTo(1);
    useDisplayStore.getState().pause();
    h.controller.goTo(2);
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.host.select).not.toHaveBeenCalled();
  });

  it('stops listening on dispose', () => {
    vi.useFakeTimers();
    const h = build();
    h.controller.dispose();
    swipe(h, 500, 200);
    expect(h.host.commit).not.toHaveBeenCalled();
    expect(h.track.style.transform).toBe('');
  });

  it('keeps the animation count balanced when disposed mid-settle', () => {
    vi.useFakeTimers();
    const h = build();
    swipe(h, 500, 200);
    expect(document.body.dataset.animating).toBe('1');
    h.controller.dispose();
    expect(document.body.dataset.animating).toBe('0');
  });

  it('follows the store when the screen changes', () => {
    vi.useFakeTimers();
    const h = build();
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 2, totalScreens: 3 } });
    expect(h.track.style.transform).toBe('translate3d(-1000px,0,0)');
    h.transitionEnd();
    expect(h.host.commit).toHaveBeenCalledWith(2);
  });

  it('reset during a settle toward another screen never commits or selects that screen', () => {
    vi.useFakeTimers();
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 2, totalScreens: 4 } });
    const h = build(2);
    swipe(h, 500, 200);
    expect(h.track.style.transform).toBe('translate3d(-1000px,0,0)');

    h.controller.reset(0);
    h.transitionEnd();
    vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
    expect(h.host.commit).not.toHaveBeenCalledWith(3);
    expect(h.host.select).not.toHaveBeenCalled();
    expect(document.body.dataset.animating).toBe('0');
  });

  it('reset sets the store current screen to the index shown', () => {
    const h = build(1);
    h.controller.reset(0);
    expect(useDisplayStore.getState().screen.currentScreen).toBe(0);
    expect(h.controller.current).toBe(0);
    expect(h.host.commit).toHaveBeenCalledWith(0);
    expect(h.host.select).not.toHaveBeenCalled();
  });

  it('accepts a new swipe after reset once React has rendered the reset screen', () => {
    vi.useFakeTimers();
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 2, totalScreens: 4 } });
    const h = build(2);
    swipe(h, 500, 200);
    h.controller.reset(0);
    h.controller.rendered(0);

    // A left drag from screen 0 goes to screen 1.
    swipe(h, 500, 200);
    h.transitionEnd();
    expect(h.host.commit).toHaveBeenLastCalledWith(1);
    expect(h.host.select).toHaveBeenCalledTimes(1);
    expect(h.host.select).toHaveBeenCalledWith(1);
  });

  describe('layout shrink', () => {
    const busy = (h: Harness): boolean => (h.controller as unknown as { isBusy(): boolean }).isBusy();

    it('resets when the total drops under a settle toward the last screen, without committing or selecting it', () => {
      vi.useFakeTimers();
      useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 2, totalScreens: 4 } });
      const h = build(2);
      swipe(h, 500, 200);
      expect(h.track.style.transform).toBe('translate3d(-1000px,0,0)');

      // The select already moved the store to 3; the layout then loses screen 3.
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 3 } });
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, totalScreens: 3 } });
      h.transitionEnd();
      vi.advanceTimersByTime(SETTLE_FALLBACK_MS);

      expect(h.host.commit).not.toHaveBeenCalledWith(3);
      expect(h.host.select).not.toHaveBeenCalledWith(3);
      expect(h.controller.current).toBeLessThan(3);
      expect(useDisplayStore.getState().screen.currentScreen).toBe(h.controller.current);
      expect(document.body.dataset.animating).toBe('0');

      h.controller.rendered(h.controller.current);
      const from = h.controller.current;
      swipe(h, 500, 200);
      h.transitionEnd();
      expect(h.host.commit).toHaveBeenLastCalledWith((from + 1) % 3);
      expect(h.host.select).toHaveBeenCalledWith((from + 1) % 3);
    });

    it('drops a queued rotate to a screen the layout no longer has', () => {
      vi.useFakeTimers();
      useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 0, totalScreens: 4 } });
      const h = build(0);
      h.down(500, 100);
      h.move(450, 100, 400);
      h.flushFrames();
      // A server rotate to 3 arrives mid-drag and is queued; then the layout shrinks to 3 screens.
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 3 } });
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 0, totalScreens: 3 } });
      h.up(450, 100, 400);
      h.transitionEnd();
      vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      h.controller.rendered(h.controller.current);
      expect(h.host.commit).not.toHaveBeenCalledWith(3);
      expect(h.controller.current).toBe(0);
    });

    it('drops a queued rotate when the server returns to the shown screen', () => {
      vi.useFakeTimers();
      useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 0, totalScreens: 4 } });
      const h = build(0);
      h.down(500, 100);
      h.move(450, 100, 400);
      h.flushFrames();
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 2 } });
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 0 } });
      h.up(450, 100, 400);
      h.transitionEnd();
      vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      h.controller.rendered(h.controller.current);
      expect(h.host.commit).not.toHaveBeenCalledWith(2);
      expect(h.controller.current).toBe(0);
    });

    it('keeps the store current screen when it is still valid', () => {
      const h = build(2);
      useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 1, totalScreens: 2 } });
      expect(h.controller.current).toBe(1);
      expect(h.host.commit).toHaveBeenCalledWith(1);
      expect(h.host.select).not.toHaveBeenCalled();
    });

    it('springs back on release when the total drops to 1 mid-drag', () => {
      vi.useFakeTimers();
      const h = build(0);
      h.down(500, 100);
      h.move(400, 100, 200);
      h.move(200, 100, 200);
      h.flushFrames();
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, totalScreens: 1 } });
      h.up(200, 100, 0);
      expect(h.track.style.transform).toBe('translate3d(0px,0,0)');

      h.transitionEnd();
      vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      expect(h.host.commit).not.toHaveBeenCalled();
      expect(h.host.select).not.toHaveBeenCalled();
      expect(busy(h)).toBe(false);
    });

    it('ignores a layout change that leaves the shown screen valid', () => {
      const h = build(1);
      useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 1, totalScreens: 5 } });
      expect(h.host.commit).not.toHaveBeenCalled();
    });

    it('leaves the controller not busy when reset names the screen React already rendered', () => {
      const h = build(1);
      h.controller.rendered(1);
      h.controller.reset(1);
      expect(busy(h)).toBe(false);
      h.controller.reset(0);
      expect(busy(h)).toBe(true);
      h.controller.rendered(0);
      expect(busy(h)).toBe(false);
    });
  });
});
