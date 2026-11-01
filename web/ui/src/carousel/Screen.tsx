import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { WidgetPlacement } from '../sse/types';
import { WidgetHost } from '../widgets/WidgetHost';
import { PaneContext, type PaneContextValue } from './PaneContext';

export interface ScreenProps {
  /** Screen index this pane shows. */
  index: number;
  /** -1 previous, 0 current, 1 next. Sets the pane's position in the track. */
  offset: -1 | 0 | 1;
  placements: readonly WidgetPlacement[];
  /** The layout has loaded. The current pane does not claim hydration before that. */
  ready: boolean;
}

/**
 * One screen as a CSS grid. Only the current pane carries `id="grid-canvas"` and the
 * e-ink hydration attributes. Neighbours are siblings outside it, so their widgets are
 * not counted by the settle predicate.
 */
export const Screen = memo(function Screen({ index, offset, placements, ready }: ScreenProps) {
  const isCurrent = offset === 0;
  const ref = useRef<HTMLDivElement>(null);
  // A pane that mounts as a neighbour adds its widgets one per idle slot, so the settle commit
  // that brings it in does not lay out a whole screen in one long task (#803). The current
  // pane always renders every widget, so the e-ink stamp still means "all widgets mounted".
  const [mounted, setMounted] = useState(() => (isCurrent ? placements.length : 0));
  const visible = isCurrent ? placements.length : Math.min(mounted, placements.length);
  useEffect(() => {
    if (isCurrent) {
      setMounted(placements.length);
      return;
    }
    if (mounted >= placements.length) return;
    return scheduleIdleStep(() => setMounted((m) => m + 1));
  }, [isCurrent, mounted, placements.length]);
  const value = useMemo<PaneContextValue>(() => ({ isCurrentPane: isCurrent, paneIndex: index }), [isCurrent, index]);

  // Layout effects run after the cell children have mounted, so the stamp means every
  // widget of the current pane is in the DOM.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (isCurrent && ready) {
      el.dataset.widgetCount = String(placements.length);
      el.dataset.screenHydrated = 'true';
    } else {
      delete el.dataset.widgetCount;
      delete el.dataset.screenHydrated;
    }
  }, [isCurrent, ready, placements, visible]);

  return (
    <PaneContext.Provider value={value}>
      <div
        ref={ref}
        className="grid-pane"
        id={isCurrent ? 'grid-canvas' : undefined}
        style={{ left: `${offset * 100}%` }}
      >
        {placements.slice(0, visible).map((placement) => (
          <WidgetHost
            key={placement.widget_id}
            id={placement.widget_id}
            origin={placement.origin}
            dimensions={placement.dimensions}
          />
        ))}
      </div>
    </PaneContext.Provider>
  );
});

/**
 * Runs `step` in the next idle slot, with a 300 ms timeout so it cannot starve. Falls back to
 * a 16 ms timer where requestIdleCallback is missing. Returns a cancel function.
 *
 * Not gated on `data-animating`: holding the step until an animation ends lands it exactly
 * when the settle commit runs, which brings the long task back (#803 bench, 4 of 5 runs).
 */
export function scheduleIdleStep(step: () => void): () => void {
  let cancelled = false;
  const run = () => {
    if (!cancelled) step();
  };
  // Both functions are read once, so the cancel matches the scheduler that queued the step.
  const ric = window.requestIdleCallback;
  const cic = window.cancelIdleCallback;
  if (typeof ric === 'function' && typeof cic === 'function') {
    const id = ric.call(window, run, { timeout: 300 });
    return () => {
      cancelled = true;
      cic.call(window, id);
    };
  }
  const id = window.setTimeout(run, 16);
  return () => {
    cancelled = true;
    window.clearTimeout(id);
  };
}
