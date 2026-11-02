import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PaneContext } from '../../carousel/PaneContext';
import { useDisplayStore } from '../../store';
import { YtMusicWidget, type MusicState } from './YtMusicWidget';

const initialStore = useDisplayStore.getState();

const mockInitialState: MusicState = {
  active_speaker: 'media_player.living_room_speaker',
  speakers: [
    { entity_id: 'media_player.living_room_speaker', name: 'Living Room' },
    { entity_id: 'media_player.kitchen_speaker', name: 'Kitchen' },
  ],
  now_playing: {
    title: 'Starboy',
    artist: 'The Weeknd',
    thumb: 'https://example.com/starboy.jpg',
    state: 'playing',
    volume: 50,
    queue_index: 0,
  },
  playlists: [
    {
      playlist_id: 'PL_favorites',
      title: 'Daily Drive',
      thumb: 'https://example.com/daily.jpg',
      count: 24,
    },
  ],
  queue: [
    {
      title: 'Starboy',
      artist: 'The Weeknd',
      thumb: 'https://example.com/starboy.jpg',
    },
    {
      title: 'Blinding Lights',
      artist: 'The Weeknd',
      thumb: 'https://example.com/blinding.jpg',
    },
  ],
};

const mockSearchResults = {
  items: [
    {
      video_id: 'vid_123',
      title: 'Save Your Tears',
      artist: 'The Weeknd',
      thumb: 'https://example.com/tears.jpg',
      duration: '3:35',
    },
  ],
};

describe('YtMusicWidget', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    useDisplayStore.setState(initialStore, true);
    useDisplayStore.setState({
      visibility: { visible: true },
      widgets: {
        'yt-1': {
          meta: {
            type: 'yt-music',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {
              api_base: '/hearth-api',
            },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          timestamp: '2026-11-01T12:00:00Z',
        },
      },
    });

    fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const urlStr = String(url);

      if (urlStr.includes('/music/state')) {
        return new Response(JSON.stringify(mockInitialState), { status: 200 });
      }

      if (urlStr.includes('/music/search')) {
        return new Response(JSON.stringify(mockSearchResults), { status: 200 });
      }

      if (urlStr.includes('/music/control')) {
        let body: Record<string, unknown> = {};
        if (init?.body) {
          body = JSON.parse(String(init.body));
        }

        if (body.action === 'select_speaker') {
          return new Response(
            JSON.stringify({
              state: {
                ...mockInitialState,
                active_speaker: body.speaker,
              },
            }),
            { status: 200 }
          );
        }

        if (body.action === 'volume') {
          return new Response(
            JSON.stringify({
              state: {
                ...mockInitialState,
                now_playing: {
                  ...mockInitialState.now_playing,
                  volume: body.value,
                },
              },
            }),
            { status: 200 }
          );
        }

        if (body.action === 'play_pause') {
          return new Response(
            JSON.stringify({
              state: {
                ...mockInitialState,
                now_playing: {
                  ...mockInitialState.now_playing,
                  state: 'paused',
                },
              },
            }),
            { status: 200 }
          );
        }

        return new Response(JSON.stringify({ state: mockInitialState }), { status: 200 });
      }

      if (urlStr.includes('/music/play')) {
        let body: Record<string, unknown> = {};
        if (init?.body) {
          body = JSON.parse(String(init.body));
        }
        return new Response(
          JSON.stringify({
            state: {
              ...mockInitialState,
              now_playing: {
                title: body.playlist_title || body.title || 'New Song',
                artist: body.artist || 'Artist',
                thumb: body.thumb || mockInitialState.now_playing?.thumb,
                state: 'playing',
              },
            },
          }),
          { status: 200 }
        );
      }

      return new Response('Not Found', { status: 404 });
    });

    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('hydrates music state on mount and renders speakers, now playing deck, and playlists', async () => {
    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    // Initial state fetch
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith('/hearth-api/music/state');
    });

    // Verify speakers rendered
    await waitFor(() => {
      const spkButtons = container.querySelectorAll('.spk-pill');
      expect(spkButtons.length).toBe(2);
      expect(spkButtons[0].textContent).toContain('Living Room');
      expect(spkButtons[0].classList.contains('active')).toBe(true);
      expect(spkButtons[1].textContent).toContain('Kitchen');
      expect(spkButtons[1].classList.contains('active')).toBe(false);
    });

    // Verify now playing deck
    const npTitle = container.querySelector('#np-title');
    const npArtist = container.querySelector('#np-artist');
    const playBtn = container.querySelector('#btn-play');
    const volSlider = container.querySelector('#vol-slider') as HTMLInputElement;

    expect(npTitle?.textContent).toBe('Starboy');
    expect(npArtist?.textContent).toBe('The Weeknd');
    expect(playBtn?.textContent).toBe('⏸'); // playing state
    expect(volSlider?.value).toBe('50');

    // Verify playlists tab content
    const mediaTiles = container.querySelectorAll('.media-tile');
    expect(mediaTiles.length).toBe(1);
    expect(mediaTiles[0].textContent).toContain('Daily Drive');
    expect(mediaTiles[0].textContent).toContain('24 tracks');
  });

  it('selects speaker and sends control POST', async () => {
    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    await waitFor(() => {
      expect(container.querySelectorAll('.spk-pill').length).toBe(2);
    });

    const kitchenSpk = container.querySelectorAll('.spk-pill')[1];
    fireEvent.click(kitchenSpk);

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const controlCall = calls.find((c) => String(c[0]).includes('/music/control'));
      expect(controlCall).toBeDefined();
      const body = JSON.parse(String(controlCall![1]?.body));
      expect(body).toEqual({
        action: 'select_speaker',
        speaker: 'media_player.kitchen_speaker',
      });
    });

    // Kitchen speaker should become active
    await waitFor(() => {
      const kitchenPill = container.querySelectorAll('.spk-pill')[1];
      expect(kitchenPill.classList.contains('active')).toBe(true);
    });
  });

  it('handles transport buttons and volume slider change', async () => {
    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    await waitFor(() => {
      expect(container.querySelector('#btn-play')).toBeDefined();
    });

    // Previous
    fireEvent.click(container.querySelector('#btn-prev')!);
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        (c) =>
          String(c[0]).includes('/music/control') &&
          JSON.parse(String(c[1].body)).action === 'prev'
      );
      expect(call).toBeDefined();
    });

    // Next
    fireEvent.click(container.querySelector('#btn-next')!);
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        (c) =>
          String(c[0]).includes('/music/control') &&
          JSON.parse(String(c[1].body)).action === 'next'
      );
      expect(call).toBeDefined();
    });

    // Stop
    fireEvent.click(container.querySelector('#btn-stop')!);
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        (c) =>
          String(c[0]).includes('/music/control') &&
          JSON.parse(String(c[1].body)).action === 'stop'
      );
      expect(call).toBeDefined();
    });

    // Play/Pause
    fireEvent.click(container.querySelector('#btn-play')!);
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        (c) =>
          String(c[0]).includes('/music/control') &&
          JSON.parse(String(c[1].body)).action === 'play_pause'
      );
      expect(call).toBeDefined();
      expect(container.querySelector('#btn-play')?.textContent).toBe('⏸');
    });

    // Volume change
    const volSlider = container.querySelector('#vol-slider') as HTMLInputElement;
    fireEvent.change(volSlider, { target: { value: '75' } });
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        (c) =>
          String(c[0]).includes('/music/control') &&
          JSON.parse(String(c[1].body)).action === 'volume'
      );
      expect(call).toBeDefined();
      expect(JSON.parse(String(call![1]?.body)).value).toBe(75);
    });
  });

  it('switches to Queue tab and triggers queue index playback', async () => {
    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    await waitFor(() => {
      expect(container.querySelector('.sub-toolbar')).toBeDefined();
    });

    const queueTabBtn = container.querySelector('button[data-tab="queue"]')!;
    fireEvent.click(queueTabBtn);

    await waitFor(() => {
      const queueTiles = container.querySelectorAll('.media-tile');
      expect(queueTiles.length).toBe(2);
      expect(queueTiles[0].textContent).toContain('Starboy');
      expect(queueTiles[1].textContent).toContain('Blinding Lights');
    });

    // Click second item in queue
    const secondTile = container.querySelectorAll('.media-tile')[1];
    fireEvent.click(secondTile);

    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        (c) =>
          String(c[0]).includes('/music/control') &&
          JSON.parse(String(c[1]?.body)).action === 'play_index'
      );
      expect(call).toBeDefined();
      const body = JSON.parse(String(call![1]?.body));
      expect(body.index).toBe(1);
    });
  });

  it('plays playlist on playlist tile click', async () => {
    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    await waitFor(() => {
      expect(container.querySelector('.media-tile')).toBeDefined();
    });

    const playlistTile = container.querySelector('.media-tile')!;
    fireEvent.click(playlistTile);

    await waitFor(() => {
      const call = fetchMock.mock.calls.find((c) => String(c[0]).includes('/music/play'));
      expect(call).toBeDefined();
      const body = JSON.parse(String(call![1]?.body));
      expect(body).toEqual({
        speaker: 'media_player.living_room_speaker',
        playlist_id: 'PL_favorites',
        playlist_title: 'Daily Drive',
        shuffle: true,
      });
    });
  });

  it('performs search and triggers playback on search result click', async () => {
    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    await waitFor(() => {
      expect(container.querySelector('#search-input')).toBeDefined();
    });

    const searchInput = container.querySelector('#search-input') as HTMLInputElement;
    const searchForm = container.querySelector('#search-form') as HTMLFormElement;

    fireEvent.change(searchInput, { target: { value: 'Save Your Tears' } });
    fireEvent.submit(searchForm);

    await waitFor(() => {
      const call = fetchMock.mock.calls.find((c) =>
        String(c[0]).includes('/music/search?q=Save%20Your%20Tears')
      );
      expect(call).toBeDefined();
    });

    // Search results should render in grid
    await waitFor(() => {
      const tiles = container.querySelectorAll('.media-tile');
      expect(tiles.length).toBe(1);
      expect(tiles[0].textContent).toContain('Save Your Tears');
      expect(tiles[0].textContent).toContain('3:35');
    });

    // Click search result to play
    fireEvent.click(container.querySelector('.media-tile')!);

    await waitFor(() => {
      const call = fetchMock.mock.calls.find((c) => {
        if (!String(c[0]).includes('/music/play')) return false;
        const b = JSON.parse(String(c[1]?.body ?? '{}'));
        return b.video_id === 'vid_123';
      });
      expect(call).toBeDefined();
      const body = JSON.parse(String(call![1]?.body));
      expect(body).toEqual({
        speaker: 'media_player.living_room_speaker',
        video_id: 'vid_123',
        title: 'Save Your Tears',
        artist: 'The Weeknd',
        thumb: 'https://example.com/tears.jpg',
      });
    });
  });

  it('pauses polling when off-pane or hidden, and resumes when active', async () => {
    vi.useFakeTimers();

    // 1. Off-pane: isCurrentPane = false
    const { rerender } = render(
      <PaneContext.Provider value={{ isCurrentPane: false, paneIndex: 1 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    expect(fetchMock).not.toHaveBeenCalled();

    // Advance 8s while off-pane: no fetch
    act(() => {
      vi.advanceTimersByTime(8000);
    });
    expect(fetchMock).not.toHaveBeenCalled();

    // 2. Active pane: fetch fires immediately
    rerender(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <YtMusicWidget id="yt-1" type="yt-music" />
      </PaneContext.Provider>
    );

    await act(async () => {
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(1); // /music/state

    // Advance 8s while active: interval re-fetches
    await act(async () => {
      vi.advanceTimersByTime(8000);
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(2);

    // 3. Document hidden: polling pauses
    act(() => {
      useDisplayStore.setState({ visibility: { visible: false } });
    });

    act(() => {
      vi.advanceTimersByTime(8000);
    });
    // Still 2 calls, no new fetch while hidden
    expect(fetchMock).toHaveBeenCalledTimes(2);

    // 4. Document visible again: polling resumes
    act(() => {
      useDisplayStore.setState({ visibility: { visible: true } });
    });

    await act(async () => {
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(3);

    vi.useRealTimers();
  });
});
