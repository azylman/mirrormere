import { useEffect, useRef } from 'react';
import { useIsCurrentPane } from '../carousel/PaneContext';
import { useWidget } from '../store/hooks';
import type { WidgetComponentProps } from './registry';
import {
  hoistTypeStylesheets,
  legacyRenderURL,
  mountHooks,
  prepareFragment,
  unavailableCard,
  unmountAll,
  type LegacyMount,
  type LegacyOnloadHook,
} from './legacyFragment';

/** Per-cell state that outlives renders: the in-flight request and what is mounted in the card. */
interface LegacyState {
  /** The timestamp the last fetch was started for; null before the first fetch and after unmount. */
  fetchedTimestamp: string | null;
  controller: AbortController | null;
  mounts: LegacyMount[];
}

/**
 * Renders a widget package that has only a legacy HTML template (custom packages in
 * /config/widgets). The fragment's card is injected into a container this component owns, so
 * React never reconciles the legacy DOM. Fetches on mount, and again when the widget's
 * timestamp moves while its pane is current. A pane that is not current keeps its content and
 * fetches once it becomes current, if the timestamp changed meanwhile.
 */
export function LegacyWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const current = useIsCurrentPane();
  const host = useRef<HTMLDivElement>(null);
  const state = useRef<LegacyState>({ fetchedTimestamp: null, controller: null, mounts: [] });
  const type = entry.meta?.type ?? '';
  const assetsBase = entry.meta?.assets_base ?? '';
  const timestamp = entry.timestamp;

  useEffect(() => {
    const s = state.current;
    return () => {
      s.controller?.abort();
      s.controller = null;
      s.fetchedTimestamp = null;
      unmountAll(s.mounts);
    };
  }, []);

  useEffect(() => {
    const s = state.current;
    if (s.fetchedTimestamp !== null && (!current || s.fetchedTimestamp === timestamp)) return;
    s.fetchedTimestamp = timestamp;
    s.controller?.abort();
    const controller = new AbortController();
    s.controller = controller;
    const el = host.current;
    const hadContent = el !== null && el.childElementCount > 0;

    const commit = (card: HTMLElement, hooks: LegacyOnloadHook[]) => {
      if (!el) return;
      // Controllers unmount before their card is replaced; mount after it is in the document.
      unmountAll(s.mounts);
      el.replaceChildren(card);
      s.mounts = mountHooks(hooks, id);
    };

    fetch(legacyRenderURL(id, window.location.search), { signal: controller.signal })
      .then(async (res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.text();
      })
      .then((html) => {
        if (controller.signal.aborted) return;
        const { card, hooks } = prepareFragment(html);
        hoistTypeStylesheets(type, assetsBase, card);
        commit(card, hooks);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        console.warn(`[mirrormere] legacy widget ${id} render failed:`, err);
        // Keep the last good card on a failed update; show the unavailable card on the first render.
        if (!hadContent) commit(unavailableCard(id), []);
      });
  }, [id, type, assetsBase, timestamp, current]);

  return <div ref={host} className="widget-legacy" />;
}
