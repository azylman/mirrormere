import { create } from 'zustand';
import type { BufferedEvent } from '../sse/batch';
import type { ConnectionPayload } from '../sse/types';
import { deepEqual } from './equal';
import { REFETCH_ON_UPDATE, fetchWidgetDetail, toWidgetEntry } from './hydrate';
import type { DisplayData, WidgetEntry } from './types';

export type {
  AudioSlice,
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
};

/** Latest request sequence per widget id, so a slow older fetch cannot overwrite a newer one. */
const hydrateSeq = new Map<string, number>();

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
        const { current_screen, total_screens, interval_seconds, widgets: placements } = event.data;
        replace('screen', {
          currentScreen: current_screen,
          totalScreens: total_screens,
          intervalSeconds: interval_seconds,
          widgets: placements,
        });
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
      default:
        // widget.reload, style.reload and system.reload are handled by the runtime, not the store.
        break;
    }
  }

  if (widgets !== null) patch.widgets = widgets;
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
    const entry = toWidgetEntry(await fetchWidgetDetail(id));
    if (hydrateSeq.get(id) !== seq) return;

    const current = get().widgets[id];
    if (current && deepEqual(current, entry)) return;
    const next: WidgetEntry = current && deepEqual(current.data, entry.data)
      ? { ...entry, data: current.data }
      : entry;
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
}));
