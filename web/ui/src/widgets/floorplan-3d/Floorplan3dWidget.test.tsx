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

const latestCameraSnapshot = (): { x: number; y: number; z: number } => {
  const lastCall = renderSpy.mock.lastCall as [ThreeModule.Scene, ThreeModule.PerspectiveCamera] | undefined;
  expect(lastCall?.[1]).toBeDefined();
  const pos = lastCall![1].position;
  return { x: pos.x, y: pos.y, z: pos.z };
};

const dist3 = (a: { x: number; y: number; z: number }, b: { x: number; y: number; z: number }): number =>
  Math.hypot(a.x - b.x, a.y - b.y, a.z - b.z);

function setupStubWebgl(): void {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(((kind: string) =>
    kind === '2d' ? null : {}) as never);
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  webglMode = 'stub';
}

function mockCanvasRect(canvas: HTMLCanvasElement): void {
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
}

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
    setupStubWebgl();

    const { container, unmount } = await renderLoadedWidget('fp-gl');
    const canvas = container.querySelector('canvas.fp3d-canvas') as HTMLCanvasElement;
    expect(canvas).toBeTruthy();
    expect(window.__floorplanSceneReady).toBe(true);
    expect(renderSpy).toHaveBeenCalled();

    mockCanvasRect(canvas);
    act(() => {
      canvas.dispatchEvent(ptr('pointermove', 99, 100, 100));
      canvas.dispatchEvent(ptr('pointerdown', 1, 260, 250));
      canvas.dispatchEvent(ptr('pointermove', 1, 261, 251));
      canvas.dispatchEvent(ptr('pointerup', 1, 260, 250));
    });
    expect((container.querySelector('.fp3d-widget') as HTMLElement).dataset.activePreset).toBe(
      'studio',
    );

    const studioTarget = { x: -4, y: 2, z: 0 };
    const afterTapPos = latestCameraSnapshot();
    expect(afterTapPos.x).toBeCloseTo(-4, 3);
    expect(afterTapPos.y).toBeCloseTo(15, 3);
    expect(afterTapPos.z).toBeCloseTo(19, 3);

    const rendersBeforeOrbit = renderSpy.mock.calls.length;
    act(() => {
      canvas.dispatchEvent(ptr('pointerdown', 2, 400, 250));
      canvas.dispatchEvent(ptr('pointermove', 2, 440, 270));
    });
    const afterOrbitPos = latestCameraSnapshot();
    expect(renderSpy.mock.calls.length).toBeGreaterThan(rendersBeforeOrbit);
    expect(dist3(afterOrbitPos, afterTapPos)).toBeGreaterThan(0.5);

    const rendersBeforePinch = renderSpy.mock.calls.length;
    act(() => {
      canvas.dispatchEvent(ptr('pointerdown', 3, 500, 300));
      canvas.dispatchEvent(ptr('pointermove', 3, 530, 320));
      canvas.dispatchEvent(ptr('pointerup', 3, 530, 320));
      canvas.dispatchEvent(ptr('pointerup', 2, 440, 270));
    });
    const afterPinchPos = latestCameraSnapshot();
    expect(renderSpy.mock.calls.length).toBeGreaterThan(rendersBeforePinch);
    expect(dist3(afterPinchPos, studioTarget)).toBeLessThan(dist3(afterOrbitPos, studioTarget) - 1);

    const rendersBeforeWheelIn = renderSpy.mock.calls.length;
    act(() => {
      canvas.dispatchEvent(new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -120 }));
    });
    const afterWheelInPos = latestCameraSnapshot();
    expect(renderSpy.mock.calls.length).toBeGreaterThan(rendersBeforeWheelIn);
    expect(dist3(afterWheelInPos, studioTarget)).toBeLessThan(dist3(afterPinchPos, studioTarget) - 0.5);

    const rendersBeforeWheelOut = renderSpy.mock.calls.length;
    act(() => {
      canvas.dispatchEvent(new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 120 }));
    });
    const afterWheelOutPos = latestCameraSnapshot();
    expect(renderSpy.mock.calls.length).toBeGreaterThan(rendersBeforeWheelOut);
    expect(dist3(afterWheelOutPos, studioTarget)).toBeGreaterThan(dist3(afterWheelInPos, studioTarget) + 0.5);

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

  it('applies entity-only updates on inline floors config via updateEntities and renderSpy without calling disposeSpy', async () => {
    setupStubWebgl();
    const inlineFloors = [
      {
        id: 'main-floor',
        label: 'Main Floor',
        rooms: [{ id: 'room-a', label: 'Room A', bounds: { xMin: -6, xMax: 6, zMin: -6, zMax: 6 } }],
      },
    ];
    const haBindings = [
      { id: 'room-a-light', entityId: 'light.room_a', metricType: 'light', label: 'Room A Light', roomId: 'room-a' },
    ];

    useDisplayStore.setState((s) => ({
      ...s,
      widgets: {
        'fp-meta-ent': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          meta: {
            type: 'floorplan-3d',
            origin: [0, 0],
            assets_base: '',
            dimensions: [4, 2],
            config: { title: 'Meta Inline', floors: inlineFloors, haBindings },
          },
          data: { entities: { 'light.room_a': { state: 'off' } } },
        },
        'fp-data-ent': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          data: {
            title: 'Data Inline',
            floors: inlineFloors,
            haBindings,
            entities: { 'light.room_a': { state: 'off' } },
          },
        },
      },
    }));

    const metaView = await renderLoadedWidget('fp-meta-ent');
    expect(screen.getByText(/Room A Light: off/i)).toBeTruthy();
    renderSpy.mockClear();
    disposeSpy.mockClear();

    act(() => {
      useDisplayStore.setState((s) => ({
        ...s,
        widgets: {
          ...s.widgets,
          'fp-meta-ent': {
            ...s.widgets['fp-meta-ent']!,
            timestamp: '2026-11-17T10:01:00Z',
            data: { entities: { 'light.room_a': { state: 'on', attributes: { brightness: 200 } } } },
          },
        },
      }));
    });

    await waitFor(() => {
      expect(screen.getByText(/Room A Light: on/i)).toBeTruthy();
    });
    expect(disposeSpy).not.toHaveBeenCalled();
    expect(renderSpy).toHaveBeenCalled();
    const metaScene = (renderSpy.mock.lastCall as [ThreeModule.Scene] | undefined)?.[0];
    expect(metaScene?.getObjectByName('floor-room-a')?.userData.lightState).toBe('on');
    metaView.unmount();

    disposeSpy.mockClear();
    const dataView = await renderLoadedWidget('fp-data-ent');
    expect(screen.getByText(/Room A Light: off/i)).toBeTruthy();
    renderSpy.mockClear();
    disposeSpy.mockClear();

    act(() => {
      useDisplayStore.setState((s) => ({
        ...s,
        widgets: {
          ...s.widgets,
          'fp-data-ent': {
            ...s.widgets['fp-data-ent']!,
            timestamp: '2026-11-17T10:02:00Z',
            data: {
              title: 'Data Inline',
              floors: inlineFloors,
              haBindings,
              entities: { 'light.room_a': { state: 'on' } },
            },
          },
        },
      }));
    });

    await waitFor(() => {
      expect(screen.getByText(/Room A Light: on/i)).toBeTruthy();
    });
    expect(disposeSpy).not.toHaveBeenCalled();
    expect(renderSpy).toHaveBeenCalled();
    const dataScene = (renderSpy.mock.lastCall as [ThreeModule.Scene] | undefined)?.[0];
    expect(dataScene?.getObjectByName('floor-room-a')?.userData.lightState).toBe('on');
    dataView.unmount();
  });

  it('clears incompatible floor-specific presets when switching floors while preserving shared presets', async () => {
    useDisplayStore.setState((s) => ({
      ...s,
      widgets: {
        'fp-floors-presets': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          meta: {
            type: 'floorplan-3d',
            origin: [0, 0],
            assets_base: '',
            dimensions: [4, 2],
            config: {
              title: 'Multi-Floor Presets',
              defaultFloorId: 'floor-a',
              defaultPresetId: 'shared-overview',
              floors: [
                {
                  id: 'floor-a',
                  label: 'Floor A',
                  level: 1,
                  rooms: [{ id: 'room-a', label: 'Room A', bounds: { xMin: -5, xMax: 5, zMin: -5, zMax: 5 } }],
                },
                {
                  id: 'floor-b',
                  label: 'Floor B',
                  level: 2,
                  rooms: [{ id: 'room-b', label: 'Room B', bounds: { xMin: -5, xMax: 5, zMin: -5, zMax: 5 } }],
                },
              ],
              cameraPresets: [
                { id: 'shared-overview', label: 'Overview', position: [0, 20, 22], target: [0, 2, 0] },
                { id: 'shared-top', label: 'Top', position: [0, 34, 0.01], target: [0, 0, 0] },
                { id: 'a-only', label: 'A Only', floorId: 'floor-a', position: [-3, 12, 14], target: [0, 1, 0] },
                { id: 'b-only', label: 'B Only', floorId: 'floor-b', position: [3, 18, 16], target: [0, 10, 0] },
              ],
            },
          },
          data: {},
        },
      },
    }));

    const { container } = await renderLoadedWidget('fp-floors-presets');
    const root = container.querySelector('.fp3d-widget') as HTMLElement;
    expect(root.dataset.activeFloor).toBe('floor-a');
    expect(root.dataset.activePreset).toBe('shared-overview');
    expect(container.querySelector('[data-preset="a-only"]')).toBeTruthy();
    expect(container.querySelector('[data-preset="b-only"]')).toBeNull();

    fireEvent.click(container.querySelector('[data-preset="a-only"]') as HTMLElement);
    expect(root.dataset.activePreset).toBe('a-only');

    fireEvent.click(screen.getByTestId('floorplan-floor-floor-b-btn'));
    expect(root.dataset.activeFloor).toBe('floor-b');
    expect(root.dataset.activePreset).toBe('shared-overview');
    expect(container.querySelector('[data-preset="b-only"]')).toBeTruthy();
    expect(container.querySelector('[data-preset="a-only"]')).toBeNull();

    fireEvent.click(screen.getByTestId('floorplan-floor-floor-a-btn'));
    expect(root.dataset.activeFloor).toBe('floor-a');
    expect(root.dataset.activePreset).toBe('shared-overview');

    fireEvent.click(container.querySelector('[data-preset="shared-top"]') as HTMLElement);
    expect(root.dataset.activePreset).toBe('shared-top');

    fireEvent.click(screen.getByTestId('floorplan-floor-floor-b-btn'));
    expect(root.dataset.activeFloor).toBe('floor-b');
    expect(root.dataset.activePreset).toBe('shared-top');

    fireEvent.click(container.querySelector('[data-preset="b-only"]') as HTMLElement);
    expect(root.dataset.activePreset).toBe('b-only');

    act(() => {
      window.__setFloorplanFloor?.('floor-a');
    });
    expect(root.dataset.activeFloor).toBe('floor-a');
    expect(root.dataset.activePreset).toBe('shared-overview');
  });

  it('captures pointers on pointerdown and clears active pointers on lostpointercapture/pointercancel without tap selection', async () => {
    setupStubWebgl();
    const { container } = await renderLoadedWidget('fp-capture');
    const root = container.querySelector('.fp3d-widget') as HTMLElement;
    const canvas = container.querySelector('canvas.fp3d-canvas') as HTMLCanvasElement;
    mockCanvasRect(canvas);

    const setCaptureSpy = vi.fn();
    const releaseCaptureSpy = vi.fn();
    Object.assign(canvas, {
      setPointerCapture: setCaptureSpy,
      releasePointerCapture: releaseCaptureSpy,
    });

    act(() => {
      canvas.dispatchEvent(ptr('pointerdown', 10, 260, 250));
      canvas.dispatchEvent(ptr('lostpointercapture', 10, 260, 250));
    });
    expect(setCaptureSpy).toHaveBeenCalledWith(10);
    expect(releaseCaptureSpy).toHaveBeenCalledWith(10);
    expect(root.dataset.activePreset).toBe('dollhouse');

    const rendersAfterLost = renderSpy.mock.calls.length;
    act(() => {
      canvas.dispatchEvent(ptr('pointermove', 10, 340, 310));
    });
    expect(renderSpy.mock.calls.length).toBe(rendersAfterLost);

    act(() => {
      canvas.dispatchEvent(ptr('pointerdown', 11, 260, 250));
      canvas.dispatchEvent(ptr('pointercancel', 11, 260, 250));
    });
    expect(setCaptureSpy).toHaveBeenCalledWith(11);
    expect(releaseCaptureSpy).toHaveBeenCalledWith(11);
    expect(root.dataset.activePreset).toBe('dollhouse');

    const rendersAfterCancel = renderSpy.mock.calls.length;
    act(() => {
      canvas.dispatchEvent(ptr('pointermove', 11, 350, 320));
    });
    expect(renderSpy.mock.calls.length).toBe(rendersAfterCancel);

    act(() => {
      canvas.dispatchEvent(ptr('pointerdown', 12, 260, 250));
      canvas.dispatchEvent(ptr('pointerup', 12, 260, 250));
    });
    expect(releaseCaptureSpy).toHaveBeenCalledWith(12);
    expect(root.dataset.activePreset).toBe('studio');
  });

  it('scopes window hooks and scene-ready state per widget ID across multiple mounted instances', async () => {
    setupStubWebgl();
    const makeConfig = (title: string) => ({
      title,
      defaultFloorId: 'floor-1',
      defaultPresetId: 'dollhouse',
      floors: [
        { id: 'floor-1', label: 'Floor 1', level: 1, rooms: [{ id: 'room-1', label: 'Room 1', bounds: { xMin: -5, xMax: 5, zMin: -5, zMax: 5 } }] },
        { id: 'floor-2', label: 'Floor 2', level: 2, rooms: [{ id: 'room-2', label: 'Room 2', bounds: { xMin: -5, xMax: 5, zMin: -5, zMax: 5 } }] },
      ],
      cameraPresets: [
        { id: 'dollhouse', label: 'Dollhouse', position: [0, 20, 22], target: [0, 2, 0] },
        { id: 'topdown', label: 'Top-Down', position: [0, 34, 0.01], target: [0, 0, 0] },
        { id: 'entryway', label: 'Entryway', position: [4, 10, 12], target: [0, 2, 0] },
      ],
    });
    useDisplayStore.setState((s) => ({
      ...s,
      widgets: {
        'fp-multi-a': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          meta: { type: 'floorplan-3d', origin: [0, 0], assets_base: '', dimensions: [4, 2], config: makeConfig('Widget A') },
          data: {},
        },
        'fp-multi-b': {
          state: 'ok',
          timestamp: '2026-11-17T10:00:00Z',
          meta: { type: 'floorplan-3d', origin: [4, 0], assets_base: '', dimensions: [4, 2], config: makeConfig('Widget B') },
          data: {},
        },
      },
    }));

    const viewA = await renderLoadedWidget('fp-multi-a');
    const viewB = await renderLoadedWidget('fp-multi-b');
    const rootA = viewA.container.querySelector('.fp3d-widget') as HTMLElement;
    const rootB = viewB.container.querySelector('.fp3d-widget') as HTMLElement;

    expect(window.__floorplanSceneReady).toBe(true);
    expect(window.__floorplanWidgets?.['fp-multi-a']?.sceneReady).toBe(true);
    expect(window.__floorplanWidgets?.['fp-multi-b']?.sceneReady).toBe(true);

    act(() => {
      window.__setFloorplanPreset?.('topdown', 'fp-multi-a');
      window.__setFloorplanFloor?.('floor-2', 'fp-multi-b');
      window.__setFloorplanPreset?.('entryway', 'missing-widget');
      window.__setFloorplanFloor?.('floor-2', 'missing-widget');
    });
    expect(rootA.dataset.activePreset).toBe('topdown');
    expect(rootA.dataset.activeFloor).toBe('floor-1');
    expect(rootB.dataset.activePreset).toBe('dollhouse');
    expect(rootB.dataset.activeFloor).toBe('floor-2');

    act(() => {
      window.__floorplanWidgets?.['fp-multi-b']?.setPreset('entryway');
    });
    expect(rootB.dataset.activePreset).toBe('entryway');
    expect(rootA.dataset.activePreset).toBe('topdown');

    viewB.unmount();
    expect(window.__floorplanSceneReady).toBe(true);
    expect(window.__floorplanWidgets?.['fp-multi-b']).toBeUndefined();
    expect(window.__floorplanWidgets?.['fp-multi-a']?.sceneReady).toBe(true);

    act(() => {
      window.__setFloorplanPreset?.('entryway');
      window.__setFloorplanFloor?.('floor-2', 'fp-multi-a');
    });
    expect(rootA.dataset.activePreset).toBe('entryway');
    expect(rootA.dataset.activeFloor).toBe('floor-2');

    const dupA = await renderLoadedWidget('fp-multi-a');
    viewA.unmount();
    expect(window.__floorplanSceneReady).toBe(true);
    expect(window.__floorplanWidgets?.['fp-multi-a']?.sceneReady).toBe(true);
    act(() => {
      window.__setFloorplanPreset?.('topdown', 'fp-multi-a');
    });
    expect((dupA.container.querySelector('.fp3d-widget') as HTMLElement).dataset.activePreset).toBe('topdown');

    dupA.unmount();
    expect(window.__floorplanSceneReady).toBeUndefined();
    expect(window.__floorplanWidgets).toBeUndefined();
    expect(window.__setFloorplanPreset).toBeUndefined();
    expect(window.__setFloorplanFloor).toBeUndefined();
  });

  it('clears loading state and renders fallback UI when dynamic chunk import rejects', async () => {
    vi.resetModules();
    vi.doMock('./buildFloorplanScene', () => {
      throw new Error('Failed to fetch dynamically imported module');
    });
    const { Floorplan3dWidget: FreshWidget } = await import('./Floorplan3dWidget');
    const { container, unmount } = render(<FreshWidget id="fp-import-err" type="floorplan-3d" />);
    expect(container.querySelector('.fp3d-widget')?.getAttribute('data-loading')).toBe('true');

    await waitFor(() => {
      const root = container.querySelector<HTMLElement>('.fp3d-widget');
      expect(root?.getAttribute('data-loading')).toBeNull();
      expect(root?.getAttribute('data-load-error')).toBe('true');
    });
    expect(screen.getByRole('region', { name: /3D Floorplan Fallback/i })).toBeTruthy();
    expect(screen.getByText(/3D floorplan unavailable/i)).toBeTruthy();

    unmount();
    vi.doUnmock('./buildFloorplanScene');
    vi.resetModules();
  });
});
