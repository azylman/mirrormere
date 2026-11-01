import { create } from 'zustand';
import type { BufferedEvent } from '../sse/batch';
import type { ConnectionPayload } from '../sse/types';
import { deepEqual } from './equal';
import type { ScreenLayout, ScreenRotatePayload } from '../sse/types';
import { REFETCH_ON_UPDATE, fetchScreenLayouts, fetchWidgetDetail, toWidgetEntry } from './hydrate';
import type { CarouselSlice, DisplayData, ScreenSlice, WidgetEntry } from './types';

export type {
  AudioSlice,
  CarouselSlice,
  DisplayData,
  HeaderSlice,
  ScreenSlice,
  SystemSlice,
  ThemeSlice,
  VideoSlice,
  VisibilitySlice,
  VoiceSlice,
  WidgetEntry,
  WidgetMeta,
} from './types';

export interface DisplayActions {
  /** Apply a batch of server events in one store update. Refetches widgets in REFETCH_ON_UPDATE. */
  applyEvents: (events: BufferedEvent[]) => void;
  /** Fill meta, state, data, viewModel and timestamp from GET api/widgets/{id}. */
  hydrateWidget: (id: string) => Promise<void>;
  setConnection: (payload: ConnectionPayload) => void;
  setDocumentVisible: (visible: boolean) => void;
  /** Fetch GET api/screens into `layout`. Only the latest request is applied. */
  loadScreens: () => Promise<void>;
  /** Queue screen.rotate instead of applying it. Used while a video takes over the carousel. */
  pause: () => void;
  /** Leave the paused state and apply the latest queued screen.rotate, if any. */
  resume: () => void;
  /** Mark `index` as the current screen without a server round trip. No-op when it already is. */
  setCurrentScreen: (index: number) => void;
}

export type DisplayStore = DisplayData & DisplayActions;

const initialData: DisplayData = {
  screen: { currentScreen: 0, totalScreens: 1, intervalSeconds: 30, widgets: [] },
  widgets: {},
  header: { timestamp: null, timezone: 'UTC', weather: null },
  theme: { theme: 'day' },
  video: { mode: 'widgets', primary: null, pip: null },
  audio: { volume: 75, muted: false },
  voice: { state: 'idle', transcript: null, reply: null, tts_engine: null, status: null },
  system: {
    connection: 'connecting',
    online: true,
    serverTime: null,
    configStatus: 'ok',
    configError: null,
    providers: {},
    bootId: null,
  },
  visibility: { visible: true },
  layout: [],
  layoutVersion: 0,
  widgetReloads: {},
  carousel: { paused: false, pendingRotate: null },
};

/**
 * True when a screen.rotate describes a layout other than `layout`: a different screen
 * count, or different widget placements on the current screen. A rotate with the same
 * placements leaves the layout alone. An empty layout is not yet known, so it is never a change.
 */
export function rotateChangesLayout(layout: readonly ScreenLayout[], data: ScreenRotatePayload): boolean {
  if (layout.length === 0) return false;
  if (data.total_screens !== layout.length) return true;
  const screen = layout.find((s) => s.index === data.current_screen);
  if (!screen) return true;
  const current = screen.widgets;
  const incoming = data.widgets;
  if (current.length !== incoming.length) return true;
  return current.some((w, i) => {
    const next = incoming[i];
    return (
      w.widget_id !== next.widget_id ||
      w.origin[0] !== next.origin[0] ||
      w.origin[1] !== next.origin[1] ||
      w.dimensions[0] !== next.dimensions[0] ||
      w.dimensions[1] !== next.dimensions[1]
    );
  });
}

function screenFromRotate(payload: ScreenRotatePayload): ScreenSlice {
  return {
    currentScreen: payload.current_screen,
    totalScreens: payload.total_screens,
    intervalSeconds: payload.interval_seconds,
    widgets: payload.widgets,
  };
}

/** Latest request sequence per widget id, so a slow older fetch cannot overwrite a newer one. */
const hydrateSeq = new Map<string, number>();
let screensSeq = 0;

/**
 * Pure reducer for a batch of events. Returns the patch to apply and the widget
 * ids that need a refetch. Unchanged slices and widgets are left out of the
 * patch so their references (and subscribers) stay untouched.
 */
export function reduceEvents(
  state: DisplayData,
  events: readonly BufferedEvent[],
): { patch: Partial<DisplayData>; refetch: string[] } {
  const patch: Partial<DisplayData> = {};
  const refetch = new Set<string>();
  let widgets: Record<string, WidgetEntry> | null = null;
  let carousel: CarouselSlice = state.carousel;
  let layoutBumps = 0;
  let reloads: Record<string, number> | null = null;

  const replace = <K extends keyof DisplayData>(key: K, value: DisplayData[K]): void => {
    if (!deepEqual(state[key], value)) patch[key] = value;
  };

  for (const event of events) {
    switch (event.type) {
      case 'widget.update': {
        const { widget_id: id, state: health, timestamp, data } = event.data;
        const current = (widgets ?? state.widgets)[id];
        if (!current || current.state !== health || !deepEqual(current.data, data)) {
          widgets ??= { ...state.widgets };
          // Keep the previous data reference when only the state changed.
          const keepData = current !== undefined && deepEqual(current.data, data);
          widgets[id] = { ...current, state: health, data: keepData ? current.data : data, timestamp };
        }
        const type = (widgets ?? state.widgets)[id]?.meta?.type;
        if (type !== undefined && REFETCH_ON_UPDATE.has(type)) refetch.add(id);
        break;
      }
      case 'screen.rotate': {
        if (rotateChangesLayout(state.layout, event.data)) layoutBumps++;
        // While paused the latest rotate is kept aside and applied by resume().
        if (carousel.paused) {
          carousel = { ...carousel, pendingRotate: event.data };
          break;
        }
        replace('screen', screenFromRotate(event.data));
        break;
      }
      case 'header.update':
        replace('header', {
          timestamp: event.data.timestamp,
          timezone: event.data.timezone,
          weather: event.data.weather ?? null,
        });
        break;
      case 'theme.update':
        replace('theme', { theme: event.data.theme });
        break;
      case 'video.state':
        replace('video', event.data);
        break;
      case 'audio.state':
        replace('audio', event.data);
        break;
      case 'voice.state':
        replace('voice', event.data);
        break;
      case 'system.status': {
        const { online, time, config_status, config_error, providers, boot_id } = event.data;
        // A config change or a new server boot can change the layout; the carousel refetches it.
        if (
          config_status !== state.system.configStatus ||
          config_error !== state.system.configError ||
          (boot_id !== undefined && state.system.bootId !== null && boot_id !== state.system.bootId)
        ) {
          layoutBumps++;
        }
        replace('system', {
          ...state.system,
          online,
          serverTime: time,
          configStatus: config_status,
          configError: config_error,
          providers: providers ?? {},
          bootId: boot_id ?? state.system.bootId,
        });
        break;
      }
      case 'widget.reload': {
        // Mounted widgets re-hydrate from their WidgetHost, which defers while the carousel is paused.
        layoutBumps++;
        const type = event.data.type;
        if (type) {
          reloads ??= { ...state.widgetReloads };
          reloads[type] = (reloads[type] ?? 0) + 1;
        }
        break;
      }
      default:
        // style.reload and system.reload are handled by the runtime, not the store.
        break;
    }
  }

  if (widgets !== null) patch.widgets = widgets;
  if (carousel !== state.carousel) patch.carousel = carousel;
  if (reloads !== null) patch.widgetReloads = reloads;
  if (layoutBumps > 0) patch.layoutVersion = state.layoutVersion + layoutBumps;
  return { patch, refetch: [...refetch] };
}

export const useDisplayStore = create<DisplayStore>()((set, get) => ({
  ...initialData,

  applyEvents(events) {
    if (events.length === 0) return;
    const { patch, refetch } = reduceEvents(get(), events);
    if (Object.keys(patch).length > 0) set(patch);
    for (const id of refetch) {
      get().hydrateWidget(id).catch((err: unknown) => {
        console.warn(`[mirrormere] widget ${id} refetch failed:`, err);
      });
    }
  },

  async hydrateWidget(id) {
    const seq = (hydrateSeq.get(id) ?? 0) + 1;
    hydrateSeq.set(id, seq);
    const before = get().widgets[id];
    const entry = toWidgetEntry(await fetchWidgetDetail(id));
    if (hydrateSeq.get(id) !== seq) return;

    // A widget.update that landed while the fetch was in flight is newer than
    // the fetch for state, data and timestamp. Keep the stream's values and take
    // only meta and viewModel from the fetch.
    const current = get().widgets[id];
    const streamed = current !== before && current !== undefined;
    const incoming: WidgetEntry = streamed
      ? { ...current, meta: entry.meta, viewModel: entry.viewModel }
      : entry;

    if (current && deepEqual(current, incoming)) return;
    const next: WidgetEntry = current && deepEqual(current.data, incoming.data)
      ? { ...incoming, data: current.data }
      : incoming;
    set((s) => ({ widgets: { ...s.widgets, [id]: next } }));
  },

  setConnection(payload) {
    const connection = payload.status === 'connected' ? 'connected' : 'disconnected';
    if (get().system.connection !== connection) {
      set((s) => ({ system: { ...s.system, connection } }));
    }
  },

  setDocumentVisible(visible) {
    if (get().visibility.visible !== visible) {
      set({ visibility: { visible } });
    }
  },

  async loadScreens() {
    const seq = ++screensSeq;
    const body = await fetchScreenLayouts();
    if (seq !== screensSeq) return;
    const layout = body.screens;
    if (deepEqual(get().layout, layout)) return;
    set((s) => ({
      layout,
      screen: s.screen.totalScreens === layout.length ? s.screen : { ...s.screen, totalScreens: layout.length },
    }));
  },

  pause() {
    const { carousel } = get();
    if (!carousel.paused) set({ carousel: { ...carousel, paused: true } });
  },

  resume() {
    const { carousel } = get();
    if (!carousel.paused) return;
    const pending = carousel.pendingRotate;
    const layoutChanged = pending !== null && rotateChangesLayout(get().layout, pending);
    set({
      carousel: { paused: false, pendingRotate: null },
      ...(pending ? { screen: screenFromRotate(pending) } : {}),
      ...(layoutChanged ? { layoutVersion: get().layoutVersion + 1 } : {}),
    });
  },

  setCurrentScreen(index) {
    if (get().screen.currentScreen === index) return;
    set((s) => ({ screen: { ...s.screen, currentScreen: index } }));
  },
}));
