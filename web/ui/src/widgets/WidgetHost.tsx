import { memo, useEffect, useRef, type CSSProperties } from 'react';
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
 * when the store has no meta for it yet. Re-fetches when a widget.reload names its
 * type; a reload that arrives while the carousel is paused waits for resume.
 */
export const WidgetHost = memo(function WidgetHost({ id, origin, dimensions }: WidgetHostProps) {
  const entry = useWidget(id);
  const type = entry.meta?.type;
  const reloads = useDisplayStore((s) => (type === undefined ? undefined : s.widgetReloads[type]));
  const paused = useDisplayStore((s) => s.carousel.paused);
  // The type and reload count this cell last acted on. Meta arrives after mount, so the
  // first time a type appears it becomes the baseline and does not count as a reload.
  const seen = useRef({ type, count: reloads ?? 0 });

  useEffect(() => {
    const store = useDisplayStore.getState();
    if (!store.widgets[id]?.meta) {
      store.hydrateWidget(id).catch((err: unknown) => {
        console.warn(`[mirrormere] widget ${id} hydrate failed:`, err);
      });
    }
  }, [id]);

  useEffect(() => {
    const count = reloads ?? 0;
    if (seen.current.type !== type) {
      seen.current = { type, count };
      return;
    }
    if (paused || count <= seen.current.count) return;
    seen.current = { type, count };
    useDisplayStore.getState().hydrateWidget(id).catch((err: unknown) => {
      console.warn(`[mirrormere] widget ${id} reload hydrate failed:`, err);
    });
  }, [id, type, reloads, paused]);

  const Component = widgetComponentFor(type);
  return (
    <div className="widget-card" data-widget-id={id} data-widget-type={type} style={cellStyle(origin, dimensions)}>
      {Component ? <Component id={id} type={type ?? ''} /> : <div className="widget-card-placeholder">{type ?? ''}</div>}
    </div>
  );
});
