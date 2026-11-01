import type {
  AudioStatePayload,
  HeaderWeather,
  ProviderHealth,
  ScreenRotateWidget,
  VideoStatePayload,
  VoiceStatePayload,
  WidgetDetailResponse,
} from '../sse/types';

export type WidgetMeta = Pick<WidgetDetailResponse, 'assets_base' | 'config' | 'origin' | 'dimensions'> & {
  type: string;
};

/** Per-widget slice. Only `state`, `data` and `timestamp` come from widget.update. */
export interface WidgetEntry {
  meta?: WidgetMeta;
  state: string;
  data: unknown;
  viewModel?: Record<string, unknown>;
  timestamp: string;
}

export interface ScreenSlice {
  currentScreen: number;
  totalScreens: number;
  intervalSeconds: number;
  widgets: ScreenRotateWidget[];
}

export interface HeaderSlice {
  timestamp: string | null;
  timezone: string;
  weather: HeaderWeather | null;
}

export interface ThemeSlice {
  theme: string;
}

export type VideoSlice = VideoStatePayload;
export type AudioSlice = AudioStatePayload;
export type VoiceSlice = VoiceStatePayload;

export interface SystemSlice {
  connection: 'connecting' | 'connected' | 'disconnected';
  online: boolean;
  serverTime: string | null;
  configStatus: 'ok' | 'error';
  configError: string | null;
  providers: Record<string, ProviderHealth>;
  bootId: string | null;
}

export interface VisibilitySlice {
  /** False while the document is hidden or the page is being unloaded. */
  visible: boolean;
}

export interface DisplayData {
  screen: ScreenSlice;
  widgets: Record<string, WidgetEntry>;
  header: HeaderSlice;
  theme: ThemeSlice;
  video: VideoSlice;
  audio: AudioSlice;
  voice: VoiceSlice;
  system: SystemSlice;
  visibility: VisibilitySlice;
}
