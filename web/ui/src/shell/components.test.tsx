import { act, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../store';
import { HeaderVideoButton } from './HeaderVideoButton';
import { StatusDot } from './StatusDot';
import { WeatherPill } from './WeatherPill';

describe('WeatherPill component', () => {
  beforeEach(() => {
    useDisplayStore.setState({
      header: { timestamp: null, timezone: 'UTC', weather: null },
    });
  });

  it('renders fallback markup when weather data is null', () => {
    const { container } = render(<WeatherPill />);
    expect(container.querySelector('#weather-temp')?.textContent).toBe('--°');
    expect(container.querySelector('#weather-location')?.textContent).toBe('Oakland');
    expect(container.querySelector('#weather-desc')?.textContent).toBe('Sunny & Mild');
    expect(container.querySelector('#weather-condition svg')).not.toBeNull();
  });

  it('renders weather details from header store', () => {
    useDisplayStore.setState({
      header: {
        timestamp: '2026-10-31T12:00:00Z',
        timezone: 'America/Los_Angeles',
        weather: {
          temperature: 68.4,
          units: '°F',
          icon: 'weather-sunny',
          condition: 'Sunny',
          location: 'Berkeley',
        },
      },
    });

    const { container } = render(<WeatherPill />);
    expect(container.querySelector('#weather-temp')?.textContent).toBe('68°F');
    expect(container.querySelector('#weather-location')?.textContent).toBe('Berkeley');
    expect(container.querySelector('#weather-desc')?.textContent).toBe('Sunny & Mild');
    const svg = container.querySelector('.mm-icon-weather-sunny');
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute('width')).toBe('26');
    expect(svg?.getAttribute('height')).toBe('26');
    expect(svg?.querySelector('circle')).not.toBeNull();
  });

  it('renders night weather condition with declarative SVG icon', () => {
    useDisplayStore.setState({
      header: {
        timestamp: '2026-10-31T12:00:00Z',
        timezone: 'America/Los_Angeles',
        weather: {
          temperature: 55,
          units: '°F',
          icon: 'weather-clear-night',
          condition: 'Clear Night',
          location: 'Oakland',
        },
      },
    });

    const { container } = render(<WeatherPill />);
    expect(container.querySelector('#weather-temp')?.textContent).toBe('55°F');
    expect(container.querySelector('#weather-desc')?.textContent).toBe('Clear');
    const svg = container.querySelector('.mm-icon-weather-clear-night');
    expect(svg).not.toBeNull();
    expect(svg?.querySelector('path')).not.toBeNull();
  });
});

describe('StatusDot component', () => {
  beforeEach(() => {
    useDisplayStore.setState({
      system: {
        connection: 'connected',
        online: true,
        serverTime: null,
        configStatus: 'ok',
        configError: null,
        providers: {},
        bootId: null,
      },
    });
  });

  it('hides jewel when system is healthy', () => {
    const { container } = render(<StatusDot />);
    const headerStatus = container.querySelector('#header-status') as HTMLDivElement;
    expect(headerStatus.style.display).toBe('none');

    const dot = container.querySelector('#system-status-dot');
    expect(dot?.classList.contains('status-ok')).toBe(true);
    expect(dot?.getAttribute('title')).toBe('System Status: Healthy');
  });

  it('displays degraded status when configStatus is error', () => {
    useDisplayStore.setState({
      system: {
        connection: 'connected',
        online: true,
        serverTime: null,
        configStatus: 'error',
        configError: 'Parse failed',
        providers: {},
        bootId: null,
      },
    });

    const { container } = render(<StatusDot />);
    const headerStatus = container.querySelector('#header-status') as HTMLDivElement;
    expect(headerStatus.style.display).toBe('');

    const dot = container.querySelector('#system-status-dot');
    expect(dot?.classList.contains('status-degraded')).toBe(true);
    expect(dot?.getAttribute('title')).toBe('Configuration Degraded: Parse failed');
  });

  it('displays error status when offline', () => {
    useDisplayStore.setState({
      system: {
        connection: 'disconnected',
        online: false,
        serverTime: null,
        configStatus: 'ok',
        configError: null,
        providers: {},
        bootId: null,
      },
    });

    const { container } = render(<StatusDot />);
    const headerStatus = container.querySelector('#header-status') as HTMLDivElement;
    expect(headerStatus.style.display).toBe('');

    const dot = container.querySelector('#system-status-dot');
    expect(dot?.classList.contains('status-error')).toBe(true);
    expect(dot?.getAttribute('title')).toBe('System Status: Offline');
  });
});

describe('HeaderVideoButton component', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.dataset.headerVideo = 'true';
    document.body.dataset.streamId = 'test-stream';
    document.body.dataset.streamUrl = 'https://stream.local/live';
    document.body.dataset.controlUrl = 'https://control.local/wake';
    document.body.dataset.type = 'webrtc';
  });

  afterEach(() => {
    vi.useRealTimers();
    delete document.body.dataset.headerVideo;
    delete document.body.dataset.streamId;
    delete document.body.dataset.streamUrl;
    delete document.body.dataset.controlUrl;
    delete document.body.dataset.type;
  });

  it('renders null when headerVideo dataset is not true', () => {
    delete document.body.dataset.headerVideo;
    const { container } = render(<HeaderVideoButton />);
    expect(container.firstChild).toBeNull();
  });

  it('triggers video wake and api/video/trigger on tap', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200 });

    const { container } = render(<HeaderVideoButton fetchFn={fetchMock as unknown as typeof fetch} />);
    const btn = container.querySelector('#header-video-btn') as HTMLButtonElement;
    expect(btn).not.toBeNull();

    await act(async () => {
      fireEvent.click(btn);
    });

    expect(fetchMock).toHaveBeenCalledWith('https://control.local/wake', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: 'test-stream', action: 'wake' }),
    });

    expect(fetchMock).toHaveBeenCalledWith('api/video/trigger', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: 'test-stream',
        stream_url: 'https://stream.local/live',
        type: 'webrtc',
        priority: 'persistent',
        controllable: true,
        control_url: 'https://control.local/wake',
      }),
    });

    expect(btn.classList.contains('loading')).toBe(true);

    // Switch video state to active video: loading state clears
    act(() => {
      useDisplayStore.setState({
        video: {
          mode: 'video',
          primary: {
            id: 'test-stream',
            stream_url: 'https://stream.local/live',
            type: 'webrtc',
            priority: 'persistent',
            controllable: true,
          },
          pip: null,
        },
      });
    });

    expect(btn.classList.contains('loading')).toBe(false);
  });

  it('applies error class when fetch rejects and clears after 1500ms', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('Network error'));

    const { container } = render(<HeaderVideoButton fetchFn={fetchMock as unknown as typeof fetch} />);
    const btn = container.querySelector('#header-video-btn') as HTMLButtonElement;

    await act(async () => {
      fireEvent.click(btn);
    });

    expect(btn.classList.contains('error')).toBe(true);

    act(() => {
      vi.advanceTimersByTime(1500);
    });

    expect(btn.classList.contains('error')).toBe(false);
  });
});
