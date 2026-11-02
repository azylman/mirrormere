import { memo, useEffect, useState } from 'react';
import { useIsCurrentPane } from '../../carousel/PaneContext';
import { useDocumentVisible } from '../../lifecycle/visibility';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';

export interface Speaker {
  entity_id: string;
  name: string;
}

export interface NowPlaying {
  title?: string;
  artist?: string;
  thumb?: string;
  state?: string;
  volume?: number;
  queue_index?: number;
}

export interface Playlist {
  playlist_id: string;
  title: string;
  thumb?: string;
  count?: number;
}

export interface QueueItem {
  title: string;
  artist: string;
  thumb?: string;
}

export interface SearchResultItem {
  video_id: string;
  title: string;
  artist: string;
  thumb?: string;
  duration?: string;
}

export interface MusicState {
  active_speaker?: string;
  speakers?: Speaker[];
  now_playing?: NowPlaying;
  playlists?: Playlist[];
  queue?: QueueItem[];
}

export type YtMusicTab = 'playlists' | 'queue' | 'search';

const DEFAULT_STATE: MusicState = {
  active_speaker: 'media_player.home_group',
  speakers: [],
  now_playing: {},
  playlists: [],
  queue: [],
};

const DEFAULT_ART = 'https://brands.home-assistant.io/_/ytube_music_player/icon.png';

/**
 * Native React widget for YouTube Music playback and multi-room speaker control.
 * Ports web/static/yt_music.html.
 */
export const YtMusicWidget = memo(function YtMusicWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const config = entry?.meta?.config as Record<string, unknown> | undefined;
  const rawApiBase = config?.api_base ?? config?.apiBase;
  const apiBase =
    typeof rawApiBase === 'string' && rawApiBase.trim() !== ''
      ? rawApiBase.trim().replace(/\/+$/, '')
      : '/hearth-api';

  const isCurrentPane = useIsCurrentPane();
  const isDocumentVisible = useDocumentVisible();
  const isActive = isCurrentPane && isDocumentVisible;

  const [stateData, setStateData] = useState<MusicState>(DEFAULT_STATE);
  const [activeTab, setActiveTab] = useState<YtMusicTab>('playlists');
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<SearchResultItem[]>([]);
  const [searching, setSearching] = useState(false);
  const [startingPlayback, setStartingPlayback] = useState(false);

  useEffect(() => {
    if (!isActive) return;

    let cancelled = false;

    const refreshState = async () => {
      try {
        const r = await fetch(`${apiBase}/music/state`);
        if (!r.ok) return;
        const data = (await r.json()) as MusicState;
        if (!cancelled && data && typeof data === 'object') {
          setStateData(data);
        }
      } catch {
        // preserve current state on network error
      }
    };

    void refreshState();

    const intervalId = window.setInterval(() => {
      void refreshState();
    }, 8000);

    return () => {
      cancelled = true;
      window.clearInterval(intervalId);
    };
  }, [isActive, apiBase]);

  const handleSelectSpeaker = async (entityId: string) => {
    setStateData((prev) => ({ ...prev, active_speaker: entityId }));
    try {
      const r = await fetch(`${apiBase}/music/control`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'select_speaker', speaker: entityId }),
      });
      const res = (await r.json().catch(() => ({}))) as { state?: MusicState };
      if (res?.state) {
        setStateData(res.state);
      }
    } catch {
      // ignore control error
    }
  };

  const sendControl = async (action: string, extra: Record<string, unknown> = {}) => {
    try {
      const r = await fetch(`${apiBase}/music/control`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action, speaker: stateData.active_speaker, ...extra }),
      });
      const res = (await r.json().catch(() => ({}))) as { state?: MusicState };
      if (res?.state) {
        setStateData(res.state);
      }
    } catch {
      // ignore control error
    }
  };

  const handleVolumeChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = Number(e.target.value);
    setStateData((prev) => ({
      ...prev,
      now_playing: { ...prev.now_playing, volume: val },
    }));
    void sendControl('volume', { value: val });
  };

  const handleSearchSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const q = searchQuery.trim();
    if (!q) return;
    setActiveTab('search');
    setSearching(true);
    try {
      const r = await fetch(`${apiBase}/music/search?q=${encodeURIComponent(q)}`);
      const data = (await r.json()) as { items?: SearchResultItem[] };
      setSearchResults(Array.isArray(data?.items) ? data.items : []);
    } catch {
      setSearchResults([]);
    } finally {
      setSearching(false);
    }
  };

  const handlePlayPlaylist = async (p: Playlist) => {
    setStartingPlayback(true);
    try {
      const r = await fetch(`${apiBase}/music/play`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          speaker: stateData.active_speaker,
          playlist_id: p.playlist_id,
          playlist_title: p.title,
          shuffle: true,
        }),
      });
      const res = (await r.json().catch(() => ({}))) as { state?: MusicState };
      if (res?.state) {
        setStateData(res.state);
      }
    } catch {
      // ignore playback error
    } finally {
      setStartingPlayback(false);
    }
  };

  const handlePlayQueueIndex = async (index: number) => {
    await sendControl('play_index', { index });
  };

  const handlePlaySearchResult = async (item: SearchResultItem) => {
    setStartingPlayback(true);
    try {
      const r = await fetch(`${apiBase}/music/play`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          speaker: stateData.active_speaker,
          video_id: item.video_id,
          title: item.title,
          artist: item.artist,
          thumb: item.thumb,
        }),
      });
      const res = (await r.json().catch(() => ({}))) as { state?: MusicState };
      if (res?.state) {
        setStateData(res.state);
      }
    } catch {
      // ignore playback error
    } finally {
      setStartingPlayback(false);
    }
  };

  const np = stateData.now_playing || {};
  const currentTitle = startingPlayback ? 'Starting playback...' : np.title || 'YouTube Music';
  const currentArtist = np.artist || (np.title ? 'Ready' : 'Pick a playlist or search a song');
  const isPlaying = np.state === 'playing';
  const volumeValue = typeof np.volume === 'number' ? np.volume : 35;

  return (
    <div className="widget-yt-music">
      {/* 1. Speaker Selector Bar */}
      <div className="speaker-bar" id="speaker-bar" style={{ touchAction: 'pan-x' }}>
        {(stateData.speakers || []).map((s) => {
          const isAct = s.entity_id === stateData.active_speaker;
          return (
            <button
              key={s.entity_id}
              type="button"
              className={`spk-pill ${isAct ? 'active' : ''}`}
              data-spk={s.entity_id}
              onClick={() => handleSelectSpeaker(s.entity_id)}
            >
              🔊 {s.name}
            </button>
          );
        })}
      </div>

      {/* 2. Now Playing Deck */}
      <div className="player-deck">
        <img
          className="art-thumb"
          id="np-art"
          src={np.thumb || DEFAULT_ART}
          alt={np.title || 'Now Playing'}
        />
        <div className="np-meta">
          <div className="np-title" id="np-title">
            {currentTitle}
          </div>
          <div className="np-artist" id="np-artist">
            {currentArtist}
          </div>
        </div>
        <div className="transport">
          <button
            type="button"
            className="ctrl-btn"
            id="btn-prev"
            title="Previous"
            onClick={() => sendControl('prev')}
          >
            ⏮
          </button>
          <button
            type="button"
            className="ctrl-btn play-main"
            id="btn-play"
            title="Play / Pause"
            onClick={() => sendControl('play_pause')}
          >
            {isPlaying ? '⏸' : '▶'}
          </button>
          <button
            type="button"
            className="ctrl-btn"
            id="btn-next"
            title="Next"
            onClick={() => sendControl('next')}
          >
            ⏭
          </button>
          <button
            type="button"
            className="ctrl-btn"
            id="btn-stop"
            title="Stop"
            onClick={() => sendControl('stop')}
          >
            ⏹
          </button>
        </div>
        <div className="vol-wrap">
          <span style={{ fontSize: '0.75rem' }} aria-hidden="true">
            🔊
          </span>
          <input
            type="range"
            className="vol-slider"
            id="vol-slider"
            min={0}
            max={100}
            value={volumeValue}
            style={{ touchAction: 'pan-x' }}
            onChange={handleVolumeChange}
          />
        </div>
      </div>

      {/* 3. Mode & Search Toolbar */}
      <div className="sub-toolbar">
        <button
          type="button"
          className={`mode-btn ${activeTab === 'playlists' ? 'active' : ''}`}
          data-tab="playlists"
          onClick={() => setActiveTab('playlists')}
        >
          Playlists
        </button>
        <button
          type="button"
          className={`mode-btn ${activeTab === 'queue' ? 'active' : ''}`}
          data-tab="queue"
          onClick={() => setActiveTab('queue')}
        >
          Queue
        </button>
        <form className="search-form" id="search-form" onSubmit={handleSearchSubmit}>
          <input
            type="text"
            className="search-input"
            id="search-input"
            placeholder="Search YouTube Music songs or artists..."
            autoComplete="off"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
          <button
            type="submit"
            className={`mode-btn ${activeTab === 'search' ? 'active' : ''}`}
          >
            Search
          </button>
        </form>
      </div>

      {/* 4. Media Grid */}
      <div className="media-grid" id="media-grid" style={{ touchAction: 'pan-y' }}>
        {activeTab === 'playlists' &&
          (stateData.playlists || []).map((p) => (
            <div
              key={p.playlist_id}
              className="media-tile"
              data-pl={p.playlist_id}
              data-title={p.title}
              onClick={() => handlePlayPlaylist(p)}
            >
              <img className="tile-img" src={p.thumb || DEFAULT_ART} loading="lazy" alt="" />
              <div className="tile-meta">
                <div className="tile-title">{p.title}</div>
                <div className="tile-sub">{Number(p.count || 0)} tracks · Shuffle</div>
              </div>
            </div>
          ))}

        {activeTab === 'queue' &&
          (stateData.queue || []).map((t, idx) => (
            <div
              key={idx}
              className="media-tile"
              data-qidx={idx}
              onClick={() => handlePlayQueueIndex(idx)}
            >
              <img className="tile-img" src={t.thumb || DEFAULT_ART} loading="lazy" alt="" />
              <div className="tile-meta">
                <div className="tile-title">
                  {idx === np.queue_index ? '▶ ' : ''}
                  {t.title}
                </div>
                <div className="tile-sub">{t.artist}</div>
              </div>
            </div>
          ))}

        {activeTab === 'search' &&
          (searching ? (
            <div style={{ fontSize: '0.78rem', opacity: 0.7 }}>Searching YouTube Music...</div>
          ) : (
            searchResults.map((t, idx) => (
              <div
                key={t.video_id || idx}
                className="media-tile"
                data-sidx={idx}
                onClick={() => handlePlaySearchResult(t)}
              >
                <img className="tile-img" src={t.thumb || DEFAULT_ART} loading="lazy" alt="" />
                <div className="tile-meta">
                  <div className="tile-title">{t.title}</div>
                  <div className="tile-sub">
                    {t.artist}
                    {t.duration ? ` · ${t.duration}` : ''}
                  </div>
                </div>
              </div>
            ))
          ))}
      </div>
    </div>
  );
});
