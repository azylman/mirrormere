import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type * as ThreeModule from 'three';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../../store';
import { widgetComponentFor } from '../registry';
import { Floorplan3dWidget } from './Floorplan3dWidget';

let webglMode: 'real' | 'stub' | 'throw' = 'real';
const renderSpy = vi.fn();
const disposeSpy = vi.fn();

vi.mock('three', async (importOriginal) => {
  const actual = await importOriginal<typeof ThreeModule>();
  class MockWebGLRenderer {
    outputColorSpace = '';
    toneMapping = 0;
    toneMappingExposure = 1;
    shadowMap = { enabled: false, type: 0 };
    constructor(opts?: unknown) {
      if (webglMode === 'throw') throw new Error('WebGL context rejected');
      if (webglMode === 'real') {
        return new actual.WebGLRenderer(opts as never) as unknown as MockWebGLRenderer;
      }
    }
    setPixelRatio = vi.fn();
    setSize = vi.fn();
    render = renderSpy;
    dispose = disposeSpy;
  }
  return { ...actual, WebGLRenderer: MockWebGLRenderer };
});

const ptr = (type: string, pointerId: number, clientX: number, clientY: number): Event =>
  Object.assign(new MouseEvent(type, { bubbles: true, cancelable: true, clientX, clientY }), {
    pointerId,
  });

async function renderLoadedWidget(id: string) {
  const view = render(<Floorplan3dWidget id={id} type="floorplan-3d" />);
  await waitFor(() => {
    expect(view.container.querySelector('.fp3d-widget')?.getAttribute('data-active-floor')).toBeTruthy();
  });
  return view;
}

afterEach(() => {
  cleanup();
  webglMode = 'real';
  renderSpy.mockClear();
  disposeSpy.mockClear();
  vi.restoreAllMocks();
  window.history.replaceState({}, '', '/');
  useDisplayStore.setState((s) => ({ ...s, widgets: {} }));
});

describe('Floorplan3dWidget — Generic Config-Driven 3D Floorplan Widget', () => {
  it('auto-registers floorplan-3d without duplicate data-widget-id and renders demo fallback, presets, fixture inspector, and HA status pills', async () => {
    expect(widgetComponentFor('floorplan-3d')).toBe(Floorplan3dWidget);

    const transient = render(<Floorplan3dWidget id="fp-transient" type="floorplan-3d" />);
    expect(transient.container.querySelector('.fp3d-widget')?.getAttribute('data-widget-id')).toBeNull();
    transient.unmount();

    window.history.replaceState({}, '', '/?floorplanPreset=topdown&kiosk=1');
    useDisplayStore.setState((s) => ({
      ...s,
      widgets: {
        fp1: {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          data: {
            entities: {
              'light.studio_main': { state: 'on' },
              'sensor.studio_temperature': {
                state: '71.2',
                attributes: { unit_of_measurement: '°F' },
              },
              'binary_sensor.entry_door': 'open',
            },
          },
        },
      },
    }));

    const { container } = await renderLoadedWidget('fp1');
    const root = container.querySelector('.fp3d-widget') as HTMLElement;
    expect(root.getAttribute('data-widget-id')).toBeNull();
    expect(root.dataset.activeFloor).toBe('floor-1');
    expect(root.dataset.activePreset).toBe('topdown');
    expect(root.dataset.kiosk).toBe('true');
    expect(
      screen.getByRole('region', { name: /Demo Studio Floorplan — Floor 1 Overview/i }),
    ).toBeTruthy();

    expect(screen.getByText(/Studio Light: on/i)).toBeTruthy();
    expect(screen.getByText(/Studio Temp: 71.2°F/i)).toBeTruthy();
    expect(screen.getByText(/Entry Light: —/i)).toBeTruthy();
    expect(screen.getByText(/Entry Door: open/i)).toBeTruthy();

    fireEvent.click(container.querySelector('[data-preset="entryway"]') as HTMLElement);
    expect(root.dataset.activePreset).toBe('entryway');

    const studioRoomBtn = container.querySelector('[data-room-id="studio"] .fp3d-fallback-room-btn') as HTMLButtonElement;
    fireEvent.click(studioRoomBtn);
    expect(root.dataset.activePreset).toBe('studio');

    const upperBtn = container.querySelector('[data-toggle="upper-floors"]') as HTMLButtonElement;
    fireEvent.click(upperBtn);
    expect(upperBtn.getAttribute('aria-pressed')).toBe('true');
    expect(root.dataset.upperFloors).toBe('true');

    const deskChip = container.querySelector('[data-fixture-id="studio-desk"]') as HTMLButtonElement;
    fireEvent.click(deskChip);
    expect(screen.getByTestId('floorplan-fixture-inspector').dataset.selectedFixture).toBe(
      'studio-desk',
    );

    fireEvent.click(screen.getByTestId('floorplan-inspector-close'));
    expect(screen.queryByTestId('floorplan-fixture-inspector')).toBeNull();

    act(() => {
      window.__setFloorplanPreset?.('dollhouse');
      window.__setFloorplanFloor?.('floor-1');
    });
    expect(root.dataset.activePreset).toBe('dollhouse');
  });

  it('loads multi-floor layout from inline config.layout JSON string, handles malformed JSON gracefully, and switches floors', async () => {
    const inlineLayout = JSON.stringify({
      title: 'Custom Two-Floor Suite',
      defaultFloorId: 'level-1',
      floors: [
        {
          id: 'level-1',
          label: 'Level 1',
          level: 1,
          rooms: [{ id: 'main-hall', label: 'Main Hall', bounds: { xMin: -6, xMax: 6, zMin: -4, zMax: 4 } }],
        },
        {
          id: 'level-2',
          label: 'Level 2',
          level: 2,
          rooms: [{ id: 'mezzanine', label: 'Mezzanine', bounds: { xMin: -6, xMax: 6, zMin: -4, zMax: 4 } }],
        },
      ],
    });

    useDisplayStore.setState((s) => ({
      ...s,
      widgets: {
        'fp-custom': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          meta: {
            type: 'floorplan-3d',
            origin: [0, 0],
            assets_base: '',
            dimensions: [4, 2],
            config: { layout: inlineLayout },
          },
          data: {},
        },
        'fp-bad-json': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          meta: {
            type: 'floorplan-3d',
            origin: [0, 0],
            assets_base: '',
            dimensions: [4, 2],
            config: { layout: '{not-valid-json' },
          },
          data: 'non-object-payload',
        },
      },
    }));

    const { container, unmount } = await renderLoadedWidget('fp-custom');
    const root = container.querySelector('.fp3d-widget') as HTMLElement;
    expect(root.dataset.activeFloor).toBe('level-1');
    expect(screen.getAllByText(/Main Hall/i).length).toBeGreaterThanOrEqual(1);

    fireEvent.click(screen.getByTestId('floorplan-floor-level-2-btn'));
    expect(root.dataset.activeFloor).toBe('level-2');
    expect(screen.getAllByText(/Mezzanine/i).length).toBeGreaterThanOrEqual(1);
    unmount();

    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation((() => {
      throw new Error('canvas context disabled');
    }) as never);
    const fallbackView = await renderLoadedWidget('fp-bad-json');
    expect(
      (fallbackView.container.querySelector('.fp3d-widget') as HTMLElement).dataset.activeFloor,
    ).toBe('floor-1');
  });

  it('fetches layout_url JSON when configured and falls back cleanly on HTTP error', async () => {
    const remoteSpec = {
      title: 'Remote Layout',
      floors: [
        {
          id: 'remote-f1',
          label: 'Remote Floor',
          rooms: [{ id: 'lab', label: 'Research Lab', bounds: { xMin: -5, xMax: 5, zMin: -5, zMax: 5 } }],
        },
      ],
    };
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => remoteSpec,
    } as Response).mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: async () => ({}),
    } as Response);

    useDisplayStore.setState((s) => ({
      ...s,
      widgets: {
        'fp-url': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          meta: {
            type: 'floorplan-3d',
            origin: [0, 0],
            assets_base: '',
            dimensions: [4, 2],
            config: { layout_url: '/config/floorplan.json' },
          },
          data: {},
        },
        'fp-url-err': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          data: { layoutUrl: '/config/unavailable.json' },
        },
      },
    }));

    const { container, unmount } = await renderLoadedWidget('fp-url');
    await waitFor(() => {
      expect((container.querySelector('.fp3d-widget') as HTMLElement).dataset.activeFloor).toBe('remote-f1');
    });
    expect(screen.getAllByText(/Research Lab/i).length).toBeGreaterThanOrEqual(1);
    unmount();

    const errView = await renderLoadedWidget('fp-url-err');
    await act(async () => { await Promise.resolve(); });
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect((errView.container.querySelector('.fp3d-widget') as HTMLElement).dataset.activeFloor).toBe('floor-1');
  });

  it('mounts WebGL viewport with orbit, pinch/wheel zoom, raycast selection, and falls back on WebGL throw', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(((kind: string) =>
      kind === '2d' ? null : {}) as never);
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
    webglMode = 'stub';

    const { container, unmount } = await renderLoadedWidget('fp-gl');
    const canvas = container.querySelector('canvas.fp3d-canvas') as HTMLCanvasElement;
    expect(canvas).toBeTruthy();
    expect(window.__floorplanSceneReady).toBe(true);
    expect(renderSpy).toHaveBeenCalled();

    vi.spyOn(canvas, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      right: 800,
      bottom: 500,
      width: 800,
      height: 500,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
    act(() => {
      canvas.dispatchEvent(ptr('pointermove', 99, 100, 100));
      canvas.dispatchEvent(ptr('pointerdown', 1, 260, 250));
      canvas.dispatchEvent(ptr('pointermove', 1, 261, 251));
      canvas.dispatchEvent(ptr('pointerup', 1, 260, 250));
      canvas.dispatchEvent(ptr('pointerdown', 2, 400, 250));
      canvas.dispatchEvent(ptr('pointermove', 2, 440, 270));
      canvas.dispatchEvent(ptr('pointerdown', 3, 500, 300));
      canvas.dispatchEvent(ptr('pointermove', 3, 530, 320));
      canvas.dispatchEvent(ptr('pointerup', 3, 530, 320));
      canvas.dispatchEvent(ptr('pointerup', 2, 440, 270));
      canvas.dispatchEvent(new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -120 }));
      canvas.dispatchEvent(new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 120 }));
    });
    expect((container.querySelector('.fp3d-widget') as HTMLElement).dataset.activePreset).toBe(
      'studio',
    );

    unmount();
    expect(disposeSpy).toHaveBeenCalled();

    webglMode = 'throw';
    render(<Floorplan3dWidget id="fp-fail" type="floorplan-3d" />);
    await waitFor(() => {
      expect(
        screen.getByRole('region', { name: /Demo Studio Floorplan — Floor 1 Overview/i }),
      ).toBeTruthy();
    });
  });
});
