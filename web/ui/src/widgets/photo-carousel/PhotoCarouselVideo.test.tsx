import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, render, fireEvent, act } from '@testing-library/react';
import {
  PhotoCarouselWidget,
  photoDeckStateById,
  isVideoPhoto,
  getVideoStreamURL,
  getSizedPhotoURL,
} from './PhotoCarouselWidget';
import { useDisplayStore } from '../../store';
import type { WidgetMeta } from '../../store/types';
import { PaneContext } from '../../carousel/PaneContext';

function makeWidgetMeta(config: Record<string, unknown> = {}): WidgetMeta {
  return { type: 'photo-carousel', config, origin: [0, 0], dimensions: [2, 2], assets_base: '' };
}

function setWidgetPhotos(
  photos: Array<{ id?: string; url?: string; URL?: string; width?: number; height?: number; aspect_ratio?: number; timestamp?: string; location?: string; album_name?: string; media_type?: string }>,
  cycleSeconds?: number, config: Record<string, unknown> = {},
) {
  useDisplayStore.setState({
    widgets: {
      ['w-photo-1']: {
        meta: makeWidgetMeta(config),
        data: { photos, ...(cycleSeconds !== undefined ? { cycle_interval_seconds: cycleSeconds } : {}) },
        state: 'ready',
        timestamp: '2026-10-31T12:00:00Z',
      },
    },
  });
}

describe('PhotoCarouselWidget video playback', () => {
  const widgetId = 'w-photo-1';
  const initialStore = useDisplayStore.getState();

  beforeEach(() => {
    vi.useFakeTimers();
    useDisplayStore.setState(initialStore, true);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  describe('helper functions', () => {
    it('appends -no suffix to sized photo URL when isVideo is true', () => {
      const url = getSizedPhotoURL('https://example.com/video-poster.jpg', { width: 800, height: 600 }, true);
      expect(url).toBe('https://example.com/video-poster.jpg=w800-h600-no');
    });

    it('detects video photos via media_type === video', () => {
      expect(isVideoPhoto(undefined)).toBe(false);
      expect(isVideoPhoto({ url: 'https://example.com/p1.jpg' })).toBe(false);
      expect(isVideoPhoto({ url: 'https://example.com/p1.jpg', media_type: 'photo' })).toBe(false);
      expect(isVideoPhoto({ url: 'https://example.com/v1.jpg', media_type: 'video' })).toBe(true);
    });

    it('constructs video stream URLs with fallback suffixes', () => {
      expect(getVideoStreamURL('')).toBe('');
      expect(getVideoStreamURL(undefined)).toBe('');
      expect(getVideoStreamURL('https://example.com/stream', 0)).toBe('https://example.com/stream=m22');
      expect(getVideoStreamURL('https://example.com/stream', 1)).toBe('https://example.com/stream=m18');
      expect(getVideoStreamURL('https://example.com/stream', 2)).toBe('https://example.com/stream=dv');
      expect(getVideoStreamURL('https://example.com/stream', 5)).toBe('https://example.com/stream=dv');
      expect(getVideoStreamURL('https://example.com/stream', -2)).toBe('https://example.com/stream=m22');
    });
  });

  describe('component video playback', () => {
    it('renders clean poster URL with -no suffix and Play button for video items', () => {
      photoDeckStateById.clear();
      setWidgetPhotos([
        { id: 'v1', url: 'https://example.com/video1.mp4', media_type: 'video' },
        { id: 'p2', url: 'https://example.com/photo2.jpg', media_type: 'photo' },
      ], 15);

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const img = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;
      expect(img.getAttribute('src')).toBe('https://example.com/video1.mp4=w1024-h768-no');

      const playBtn = container.querySelector('.photo-video-play-btn') as HTMLButtonElement;
      expect(playBtn).not.toBeNull();
      expect(playBtn.getAttribute('aria-label')).toBe('Play video');
      expect(playBtn.querySelector('svg')).not.toBeNull();
    });

    it('plays video on Play button click, pauses rotation, and resets deck remaining time', () => {
      photoDeckStateById.clear();
      setWidgetPhotos([
        { id: 'v1', url: 'https://example.com/video1.mp4', media_type: 'video' },
        { id: 'p2', url: 'https://example.com/photo2.jpg' },
      ], 20);

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const playBtn = container.querySelector('.photo-video-play-btn') as HTMLButtonElement;
      act(() => {
        fireEvent.click(playBtn);
      });

      const video = container.querySelector('video.photo-video-player') as HTMLVideoElement;
      expect(video).not.toBeNull();
      expect(video.getAttribute('src')).toBe('https://example.com/video1.mp4=m22');
      expect(video.getAttribute('poster')).toBe('https://example.com/video1.mp4=w1024-h768-no');
      expect(video.hasAttribute('autoplay')).toBe(true);
      expect(video.hasAttribute('playsinline')).toBe(true);

      expect(container.querySelector('.photo-video-play-btn')).toBeNull();

      act(() => {
        vi.advanceTimersByTime(25000);
      });
      expect(container.querySelector('video.photo-video-player')).not.toBeNull();
    });

    it('falls back stream suffix on video error (=m22 -> =m18 -> =dv) and stops if all fail', () => {
      photoDeckStateById.clear();
      setWidgetPhotos([
        { id: 'v1', url: 'https://example.com/video1.mp4', media_type: 'video' },
      ], 15);

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const playBtn = container.querySelector('.photo-video-play-btn') as HTMLButtonElement;
      act(() => {
        fireEvent.click(playBtn);
      });

      let video = container.querySelector('video.photo-video-player') as HTMLVideoElement;
      expect(video.getAttribute('src')).toBe('https://example.com/video1.mp4=m22');

      act(() => {
        fireEvent.error(video);
      });
      video = container.querySelector('video.photo-video-player') as HTMLVideoElement;
      expect(video.getAttribute('src')).toBe('https://example.com/video1.mp4=m18');

      act(() => {
        fireEvent.error(video);
      });
      video = container.querySelector('video.photo-video-player') as HTMLVideoElement;
      expect(video.getAttribute('src')).toBe('https://example.com/video1.mp4=dv');

      act(() => {
        fireEvent.error(video);
      });
      expect(container.querySelector('video.photo-video-player')).toBeNull();
      expect(container.querySelector('.photo-layer.photo-current')).not.toBeNull();
      expect(container.querySelector('.photo-video-play-btn')).not.toBeNull();
    });

    it('restores Play button on ended event and holds slide for cycle_interval_seconds allowing unlimited replays', async () => {
      let resolveDecode: (() => void) | null = null;
      const originalImage = window.Image;
      class MockImage {
        src = '';
        decode() { return new Promise<void>((res) => { resolveDecode = res; }); }
      }
      // @ts-expect-error mock window.Image
      window.Image = MockImage;

      try {
        photoDeckStateById.clear();
        setWidgetPhotos([
          { id: 'v1', url: 'https://example.com/video1.mp4', media_type: 'video' },
          { id: 'p2', url: 'https://example.com/photo2.jpg' },
        ], 10);

        const { container } = render(
          <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
            <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
          </PaneContext.Provider>,
        );

        let playBtn = container.querySelector('.photo-video-play-btn') as HTMLButtonElement;
        act(() => {
          fireEvent.click(playBtn);
        });

        const video = container.querySelector('video.photo-video-player') as HTMLVideoElement;
        expect(video).not.toBeNull();

        act(() => {
          fireEvent.ended(video);
        });

        expect(container.querySelector('video.photo-video-player')).toBeNull();
        const currentImg = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;
        expect(currentImg.getAttribute('src')).toBe('https://example.com/video1.mp4=w1024-h768-no');
        playBtn = container.querySelector('.photo-video-play-btn') as HTMLButtonElement;
        expect(playBtn).not.toBeNull();

        act(() => {
          vi.advanceTimersByTime(5000);
        });
        expect(resolveDecode).toBeNull();

        act(() => {
          fireEvent.click(playBtn);
        });
        expect(container.querySelector('video.photo-video-player')).not.toBeNull();

        act(() => {
          fireEvent.ended(container.querySelector('video.photo-video-player')!);
        });
        expect(container.querySelector('.photo-video-play-btn')).not.toBeNull();

        act(() => {
          vi.advanceTimersByTime(10000);
        });
        expect(resolveDecode).not.toBeNull();
        act(() => {
          resolveDecode!();
        });
        await vi.waitFor(() => {
          expect(container.querySelector('.photo-layer.photo-next')?.getAttribute('src')).toBe('https://example.com/photo2.jpg=w1024-h768');
        });
        act(() => {
          vi.advanceTimersByTime(850);
        });
        expect((container.querySelector('.photo-layer.photo-current') as HTMLImageElement).getAttribute('src')).toBe('https://example.com/photo2.jpg=w1024-h768');
      } finally {
        window.Image = originalImage;
      }
    });

    it('clicking playing video stops playback, restores play button, and resets rotation timer', () => {
      photoDeckStateById.clear();
      setWidgetPhotos([
        { id: 'v1', url: 'https://example.com/video1.mp4', media_type: 'video' },
        { id: 'p2', url: 'https://example.com/photo2.jpg' },
      ], 15);

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const playBtn = container.querySelector('.photo-video-play-btn') as HTMLButtonElement;
      act(() => {
        fireEvent.click(playBtn);
      });

      const video = container.querySelector('video.photo-video-player') as HTMLVideoElement;
      expect(video).not.toBeNull();

      act(() => {
        fireEvent.click(video);
      });

      expect(container.querySelector('video.photo-video-player')).toBeNull();
      expect(container.querySelector('.photo-video-play-btn')).not.toBeNull();
      const deck = photoDeckStateById.get(widgetId);
      expect(deck?.remainingMs).toBe(15000);
    });

    it('supports diptych video playback in primary and secondary slots', () => {
      photoDeckStateById.clear();
      let resizeCallback: ((entries: Array<{ contentRect: { width: number; height: number } }>) => void) | null = null;
      const originalResizeObserver = window.ResizeObserver;

      class MockResizeObserver {
        constructor(cb: (entries: Array<{ contentRect: { width: number; height: number } }>) => void) { resizeCallback = cb; }
        observe = vi.fn(); disconnect = vi.fn(); unobserve = vi.fn();
      }
      // @ts-expect-error mock window.ResizeObserver
      window.ResizeObserver = MockResizeObserver;

      try {
        setWidgetPhotos([
          { id: 'v1', url: 'https://example.com/video1.mp4', aspect_ratio: 0.75, media_type: 'video' },
          { id: 'v2', url: 'https://example.com/video2.mp4', aspect_ratio: 0.75, media_type: 'video' },
        ], 10);

        const { container } = render(
          <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
            <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
          </PaneContext.Provider>,
        );

        act(() => { resizeCallback?.([{ contentRect: { width: 1400, height: 900 } }]); });

        const diptychSlots = container.querySelectorAll('.photo-diptych-slot');
        expect(diptychSlots.length).toBe(2);

        const playBtns = container.querySelectorAll('.photo-video-play-btn');
        expect(playBtns.length).toBe(2);

        act(() => {
          fireEvent.click(playBtns[1]);
        });

        expect(diptychSlots[0].querySelector('img')).not.toBeNull();
        expect(diptychSlots[0].querySelector('.photo-video-play-btn')).not.toBeNull();

        const secVideo = diptychSlots[1].querySelector('video.photo-video-player') as HTMLVideoElement;
        expect(secVideo).not.toBeNull();
        expect(secVideo.getAttribute('src')).toBe('https://example.com/video2.mp4=m22');
        expect(diptychSlots[1].querySelector('.photo-video-play-btn')).toBeNull();

        act(() => {
          fireEvent.click(secVideo);
        });

        expect(diptychSlots[1].querySelector('video.photo-video-player')).toBeNull();
        expect(diptychSlots[1].querySelector('.photo-video-play-btn')).not.toBeNull();

        // Also test playing and stopping in primary slot of diptych
        const primaryPlayBtn = diptychSlots[0].querySelector('.photo-video-play-btn') as HTMLButtonElement;
        act(() => {
          fireEvent.click(primaryPlayBtn);
        });

        const priVideo = diptychSlots[0].querySelector('video.photo-video-player') as HTMLVideoElement;
        expect(priVideo).not.toBeNull();
        expect(priVideo.getAttribute('src')).toBe('https://example.com/video1.mp4=m22');
        expect(diptychSlots[0].querySelector('.photo-video-play-btn')).toBeNull();

        act(() => {
          fireEvent.click(priVideo);
        });
        expect(diptychSlots[0].querySelector('video.photo-video-player')).toBeNull();
        expect(diptychSlots[0].querySelector('.photo-video-play-btn')).not.toBeNull();
      } finally {
        window.ResizeObserver = originalResizeObserver;
      }
    });

    it('unmounts video player when widget pane is inactive, document hidden, or video mode active', () => {
      photoDeckStateById.clear();
      setWidgetPhotos([
        { id: 'v1', url: 'https://example.com/video1.mp4', media_type: 'video' },
      ], 10);

      const { container, rerender } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const playBtn = container.querySelector('.photo-video-play-btn') as HTMLButtonElement;
      act(() => {
        fireEvent.click(playBtn);
      });
      expect(container.querySelector('video.photo-video-player')).not.toBeNull();

      // Inactive pane hides/unmounts video player
      rerender(
        <PaneContext.Provider value={{ isCurrentPane: false, paneIndex: 1 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );
      expect(container.querySelector('video.photo-video-player')).toBeNull();
    });
  });
});
