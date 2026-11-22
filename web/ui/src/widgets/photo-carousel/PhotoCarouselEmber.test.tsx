import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, act } from '@testing-library/react';
import { PhotoCarouselWidget } from './PhotoCarouselWidget';
import type { PhotoAlbumConfig } from './photoCarouselHelpers';
import { useDisplayStore } from '../../store';
import { renderWidget, seedWidget, widgetMeta } from '../../test-utils/widget';

describe('PhotoCarouselWidget ember_bridge sync and empty states', () => {
  const widgetId = 'w-photo-1';
  const originalEmberBridge = window.EmberBridge;
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers();
    fetchMock = vi.fn().mockResolvedValue({ ok: true });
    window.fetch = fetchMock as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.clearAllTimers();
    window.EmberBridge = originalEmberBridge;
    delete window.onEmberPhotosSynced;
  });

  const album1 = 'https://photos.app.goo.gl/album1';
  type BridgeMethod = 'connectGooglePhotos' | 'syncGooglePhotosAlbum';
  const sourceTests: {
    name: string;
    config: Record<string, unknown>;
    data: Record<string, unknown>;
    bridge: BridgeMethod[];
    wantTitle: string;
    wantConnectBtn: string | null;
    wantSyncUrl: string | null;
  }[] = [
    { name: 'share_url source', config: { source: 'share_url' }, data: { photos: [] }, bridge: [], wantTitle: 'No photos in shared album', wantConnectBtn: null, wantSyncUrl: null },
    {
      name: 'ember_bridge source without a bridge',
      config: { source: 'ember_bridge', album_url: 'https://photos.app.goo.gl/test' },
      data: { photos: [] },
      bridge: [],
      wantTitle: 'No photos available',
      wantConnectBtn: null,
      wantSyncUrl: null,
    },
    {
      name: 'ember_bridge source with connectGooglePhotos',
      config: { source: 'ember_bridge', album_url: album1 },
      data: { photos: [] },
      bridge: ['connectGooglePhotos'],
      wantTitle: 'No photos available',
      wantConnectBtn: album1,
      wantSyncUrl: null,
    },
    {
      name: 'albums entry with literal ember_bridge url',
      config: { albums: [{ name: 'Ember Album', source: 'ember_bridge', url: 'https://photos.app.goo.gl/literal-ember' }] satisfies PhotoAlbumConfig[] },
      data: { photos: [] },
      bridge: ['syncGooglePhotosAlbum'],
      wantTitle: 'No photos available',
      wantConnectBtn: null,
      wantSyncUrl: 'https://photos.app.goo.gl/literal-ember',
    },
    {
      name: 'albums entry without url falls back to data.album_url',
      config: { albums: [{ name: 'Env Album', source: 'ember_bridge' }] satisfies PhotoAlbumConfig[] },
      data: { photos: [], album_url: 'https://photos.app.goo.gl/from-backend-data' },
      bridge: ['syncGooglePhotosAlbum'],
      wantTitle: 'No photos available',
      wantConnectBtn: null,
      wantSyncUrl: 'https://photos.app.goo.gl/from-backend-data',
    },
    {
      name: 'albums entry with share_url source',
      config: { albums: [{ name: 'Shared 1', source: 'share_url', url: 'https://photos.app.goo.gl/shared1' }] satisfies PhotoAlbumConfig[] },
      data: { photos: [] },
      bridge: [],
      wantTitle: 'No photos in shared album',
      wantConnectBtn: null,
      wantSyncUrl: null,
    },
  ];
  for (const tc of sourceTests) {
    it(`resolves source: ${tc.name}`, () => {
      const mocks = { connectGooglePhotos: vi.fn<(albumUrl: string) => void>(), syncGooglePhotosAlbum: vi.fn<(albumUrl: string) => void>() };
      if (tc.bridge.length > 0) {
        const bridge: NonNullable<typeof window.EmberBridge> = {};
        for (const m of tc.bridge) bridge[m] = mocks[m];
        window.EmberBridge = bridge;
      }
      seedWidget(widgetId, widgetMeta('photo-carousel', tc.config), tc.data);

      const { container } = renderWidget(PhotoCarouselWidget, widgetId, 'photo-carousel');

      expect(container.querySelector('.photo-empty .empty-title')?.textContent).toBe(tc.wantTitle);
      const btn = container.querySelector<HTMLButtonElement>('.photo-connect-btn');
      if (tc.wantConnectBtn === null) {
        expect(btn).toBeNull();
      } else {
        expect(btn?.textContent).toBe('Connect Google Photos');
        fireEvent.click(btn!);
        expect(mocks.connectGooglePhotos).toHaveBeenCalledWith(tc.wantConnectBtn);
      }
      if (tc.wantSyncUrl === null) expect(mocks.syncGooglePhotosAlbum).not.toHaveBeenCalled();
      else expect(mocks.syncGooglePhotosAlbum).toHaveBeenCalledWith(tc.wantSyncUrl);
    });
  }

  it('triggers syncGooglePhotosAlbum on mount and every 5 minutes (300_000ms)', () => {
    const syncMock = vi.fn();
    window.EmberBridge = {
      syncGooglePhotosAlbum: syncMock,
    };

    seedWidget(widgetId, widgetMeta('photo-carousel', { source: 'ember_bridge', album_url: 'https://photos.app.goo.gl/album1' }), { photos: [{ url: 'https://example.com/1.jpg' }] });

    const { unmount } = renderWidget(PhotoCarouselWidget, widgetId, 'photo-carousel');

    expect(syncMock).toHaveBeenCalledTimes(1);
    expect(syncMock).toHaveBeenCalledWith('https://photos.app.goo.gl/album1');

    act(() => {
      vi.advanceTimersByTime(300_000);
    });
    expect(syncMock).toHaveBeenCalledTimes(2);

    act(() => {
      vi.advanceTimersByTime(300_000);
    });
    expect(syncMock).toHaveBeenCalledTimes(3);

    unmount();
    act(() => {
      vi.advanceTimersByTime(300_000);
    });
    expect(syncMock).toHaveBeenCalledTimes(3);
  });

  it('handles window.onEmberPhotosSynced for needs_auth, sync error, and photo push POST', async () => {
    const syncMock = vi.fn();
    window.EmberBridge = {
      syncGooglePhotosAlbum: syncMock,
    };

    seedWidget(widgetId, widgetMeta('photo-carousel', { source: 'ember_bridge', album_url: 'https://photos.app.goo.gl/album1' }), { photos: [] });

    const { container } = renderWidget(PhotoCarouselWidget, widgetId, 'photo-carousel');

    expect(typeof window.onEmberPhotosSynced).toBe('function');

    // 1. needs_auth payload
    act(() => {
      window.onEmberPhotosSynced?.(JSON.stringify({ needs_auth: true }));
    });
    expect(container.querySelector('.empty-title')?.textContent).toBe('Google Photos sign-in required');

    // 2. ok: false payload
    act(() => {
      window.onEmberPhotosSynced?.({ ok: false, error: 'Network timeout' });
    });
    expect(container.querySelector('.empty-title')?.textContent).toBe('Photo sync failed');

    // 3. success payload with photos
    act(() => {
      window.onEmberPhotosSynced?.({
        ok: true,
        photos: ['https://lh3.googleusercontent.com/photo1', 'https://lh3.googleusercontent.com/photo2'],
      });
    });

    expect(fetchMock).toHaveBeenCalledWith(
      'api/widgets/w-photo-1/photos',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          photos: ['https://lh3.googleusercontent.com/photo1', 'https://lh3.googleusercontent.com/photo2'],
        }),
      }),
    );

    // Clears needsAuth and syncError
    expect(container.querySelector('.empty-title')?.textContent).toBe('No photos available');
  });

  it('unregisters window.onEmberPhotosSynced when the last listener unmounts', () => {
    window.EmberBridge = {
      syncGooglePhotosAlbum: vi.fn(),
    };

    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: widgetMeta('photo-carousel', { source: 'ember_bridge', album_url: 'https://photos.app.goo.gl/album1' }),
          data: { photos: [] },
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
        ['w-photo-2']: {
          meta: widgetMeta('photo-carousel', { source: 'ember_bridge', album_url: 'https://photos.app.goo.gl/album2' }),
          data: { photos: [] },
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { unmount: unmount1 } = renderWidget(PhotoCarouselWidget, widgetId, 'photo-carousel');

    const { unmount: unmount2 } = renderWidget(PhotoCarouselWidget, 'w-photo-2', 'photo-carousel');

    expect(typeof window.onEmberPhotosSynced).toBe('function');

    unmount1();
    // Still present because w-photo-2 is mounted
    expect(typeof window.onEmberPhotosSynced).toBe('function');

    unmount2();
    // Deleted because all listeners unmounted
    expect(window.onEmberPhotosSynced).toBeUndefined();
  });
});
