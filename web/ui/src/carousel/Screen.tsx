import { memo, useLayoutEffect, useMemo, useRef } from 'react';
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

  return (
    <PaneContext.Provider value={value}>
      <div
        ref={ref}
        className="grid-pane"
        id={isCurrent ? 'grid-canvas' : undefined}
        style={{ left: `${offset * 100}%` }}
      >
        {placements.map((placement) => (
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
