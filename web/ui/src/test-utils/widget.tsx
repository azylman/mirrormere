import { render } from '@testing-library/react';
import type { ComponentType } from 'react';
import { PaneContext, type PaneContextValue } from '../carousel/PaneContext';
import { useDisplayStore } from '../store';
import type { WidgetMeta } from '../store/types';

/** Widget meta at the grid origin with `config` and `dims` (default 2x2) and no assets base. */
export function widgetMeta(type: string, config: Record<string, unknown> = {}, dims: [number, number] = [2, 2]): WidgetMeta {
  return { type, config, origin: [0, 0], dimensions: dims, assets_base: '' };
}

/** Replaces the store's widgets with one entry `id` (state defaults to ready, fixed timestamp). */
export function seedWidget(id: string, meta: WidgetMeta, data: unknown, state = 'ready'): void {
  useDisplayStore.setState({ widgets: { [id]: { meta, data, state, timestamp: '2026-10-31T12:00:00Z' } } });
}

/** Renders `Comp` inside a PaneContext (current pane 0 unless `pane` overrides it). */
export function renderWidget(
  Comp: ComponentType<{ id: string; type: string }>,
  id: string,
  type: string,
  { pane }: { pane?: Partial<PaneContextValue> } = {},
) {
  const value: PaneContextValue = { isCurrentPane: true, paneIndex: 0, ...pane };
  return render(
    <PaneContext.Provider value={value}>
      <Comp id={id} type={type} />
    </PaneContext.Provider>,
  );
}
