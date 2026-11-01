import { memo, startTransition, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
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

/** Longest a neighbour pane waits before it mounts its widgets. */
export const NEIGHBOUR_FILL_TIMEOUT_MS = 300;

/** Runs `cb` when the browser is idle, or after the timeout where requestIdleCallback is missing. Returns the cancel. */
function whenIdle(cb: () => void): () => void {
  if (typeof requestIdleCallback === 'function') {
    const id = requestIdleCallback(cb, { timeout: NEIGHBOUR_FILL_TIMEOUT_MS });
    return () => cancelIdleCallback(id);
  }
  const id = setTimeout(cb, NEIGHBOUR_FILL_TIMEOUT_MS);
  return () => clearTimeout(id);
}

/**
 * One screen as a CSS grid. Only the current pane carries `id="grid-canvas"` and the
 * e-ink hydration attributes. Neighbours are siblings outside it, so their widgets are
 * not counted by the settle predicate.
 *
 * A pane that mounts as a neighbour renders its grid empty and mounts the widgets when the
 * browser is idle, as a transition, so a swipe's settle does not also pay for a whole pane of
 * widgets. A pane that is current always renders its widgets at once, and a pane that has
 * filled keeps them when it later moves back to a neighbour slot.
 */
export const Screen = memo(function Screen({ index, offset, placements, ready }: ScreenProps) {
  const isCurrent = offset === 0;
  const ref = useRef<HTMLDivElement>(null);
  const [filled, setFilled] = useState(isCurrent);
  const showWidgets = isCurrent || filled;
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
  }, [isCurrent, ready, placements]);

  useEffect(() => {
    if (showWidgets) return;
    // The cleanup cancels the fill when the pane becomes current first: it renders at once then.
    return whenIdle(() => startTransition(() => setFilled(true)));
  }, [showWidgets]);

  return (
    <PaneContext.Provider value={value}>
      <div
        ref={ref}
        className="grid-pane"
        id={isCurrent ? 'grid-canvas' : undefined}
        style={{ left: `${offset * 100}%` }}
      >
        {showWidgets && placements.map((placement) => (
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
