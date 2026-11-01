import { memo, useEffect, type CSSProperties } from 'react';
import { useDisplayStore } from '../store';
import { useWidget } from '../store/hooks';
import { widgetComponentFor } from './registry';

export interface WidgetHostProps {
  id: string;
  origin: [number, number];
  dimensions: [number, number];
}

/** Grid cell style from a placement, as the legacy carousel sets it (1-indexed grid lines). */
export function cellStyle(origin: [number, number], dimensions: [number, number]): CSSProperties {
  const colSpan = dimensions[0] || 1;
  const rowSpan = dimensions[1] || 1;
  return {
    gridColumn: `${origin[0] + 1} / span ${colSpan}`,
    gridRow: `${origin[1] + 1} / span ${rowSpan}`,
  };
}

/**
 * One widget cell. Renders its card on the first commit, so the e-ink settle
 * predicate sees the cell before the data arrives. Fetches the widget detail once,
 * when the store has no meta for it yet.
 */
export const WidgetHost = memo(function WidgetHost({ id, origin, dimensions }: WidgetHostProps) {
  const entry = useWidget(id);
  const type = entry.meta?.type;

  useEffect(() => {
    const store = useDisplayStore.getState();
    if (!store.widgets[id]?.meta) {
      store.hydrateWidget(id).catch((err: unknown) => {
        console.warn(`[mirrormere] widget ${id} hydrate failed:`, err);
      });
    }
  }, [id]);

  const Component = widgetComponentFor(type);
  return (
    <div className="widget-card" data-widget-id={id} data-widget-type={type} style={cellStyle(origin, dimensions)}>
      {Component ? <Component id={id} type={type ?? ''} /> : <div className="widget-card-placeholder">{type ?? ''}</div>}
    </div>
  );
});
