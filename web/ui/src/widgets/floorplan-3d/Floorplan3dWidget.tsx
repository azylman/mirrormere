import './Floorplan3dWidget.css';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type * as ThreeModule from 'three';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import type * as SceneModule from './buildFloorplanScene';
import type * as SpecModule from './floorplanSpec';

type Object3D = ThreeModule.Object3D;
type FloorplanEntityMap = SceneModule.FloorplanEntityMap;
type FloorplanEntityStateLike = SceneModule.FloorplanEntityStateLike;
type CameraPresetSpec = SpecModule.CameraPresetSpec;
type FloorSpec = SpecModule.FloorSpec;
type FloorplanLayoutSpec = SpecModule.FloorplanLayoutSpec;
type HaEntityBindingSpec = SpecModule.HaEntityBindingSpec;

declare global {
  interface Window {
    __setFloorplanPreset?: (presetId: string) => void;
    __setFloorplanFloor?: (floorId: string) => void;
    __floorplanSceneReady?: boolean;
  }
}

interface FloorplanDeps { three: typeof ThreeModule; scene: typeof SceneModule; spec: typeof SpecModule }
const clamp = (v: number, lo: number, hi: number): number => Math.min(hi, Math.max(lo, v));

function readQueryParam(...keys: string[]): string | undefined {
  const params = new URLSearchParams(window.location.search);
  for (const key of keys) {
    const val = params.get(key)?.trim();
    if (val) return val;
  }
  return undefined;
}

function canCreateWebgl(isUnmockedJsdom: () => boolean): boolean {
  if (isUnmockedJsdom()) return false;
  try {
    const c = document.createElement('canvas');
    return Boolean(c.getContext('webgl2') || c.getContext('webgl'));
  } catch {
    return false;
  }
}

const LAYOUT_KEYS = ['title', 'defaultFloorId', 'defaultPresetId', 'floors', 'cameraPresets', 'haBindings', 'layout'] as const;

function serializeInlineLayout(hasMetaLayout: boolean, source: unknown): string {
  if (!source || typeof source !== 'object' || Array.isArray(source)) return '';
  let candidate: unknown = source;
  if (!hasMetaLayout) {
    const src = source as Record<string, unknown>;
    const picked: Record<string, unknown> = {};
    for (const key of LAYOUT_KEYS) {
      if (key in src) picked[key] = src[key];
    }
    if (Object.keys(picked).length === 0) return '';
    candidate = picked;
  }
  try {
    return JSON.stringify(candidate);
  } catch {
    return '';
  }
}

function parseInlineLayoutKey(
  key: string,
  parseFloorplanConfig: typeof SpecModule.parseFloorplanConfig,
  fallback: FloorplanLayoutSpec,
): FloorplanLayoutSpec {
  if (!key) return fallback;
  try {
    const parsed = JSON.parse(key) as unknown;
    if (parsed && typeof parsed === 'object' && 'layout' in parsed) {
      const rawLayout = (parsed as Record<string, unknown>).layout;
      if (typeof rawLayout === 'string') {
        try { return parseFloorplanConfig({ ...parsed, layout: JSON.parse(rawLayout) as unknown }); } catch { /* fall through */ }
      }
    }
    return parseFloorplanConfig(parsed);
  } catch {
    return fallback;
  }
}

function resolveLayoutUrl(metaConfig: Record<string, unknown> | undefined, data: unknown): string | undefined {
  const dataRec = data && typeof data === 'object' ? (data as Record<string, unknown>) : undefined;
  const raw = metaConfig?.layout_url ?? metaConfig?.layoutUrl ?? dataRec?.layout_url ?? dataRec?.layoutUrl;
  return typeof raw === 'string' && raw.trim().length > 0 ? raw.trim() : undefined;
}

function extractEntities(data: unknown): FloorplanEntityMap {
  if (!data || typeof data !== 'object') return {};
  const rec = data as Record<string, unknown>;
  return (rec.entities && typeof rec.entities === 'object' ? rec.entities : rec) as FloorplanEntityMap;
}

function readBindingState(states: FloorplanEntityMap, binding: HaEntityBindingSpec): FloorplanEntityStateLike | undefined {
  const raw = states[binding.entityId] ?? states[binding.id];
  return raw === undefined ? undefined : typeof raw === 'string' ? { state: raw } : raw;
}

function formatHudPill(
  binding: HaEntityBindingSpec,
  entry: FloorplanEntityStateLike | undefined,
): { key: string; entityId: string; label: string; active: boolean } {
  if (!entry) return { key: binding.id, entityId: binding.entityId, label: `${binding.label}: —`, active: false };
  const st = entry.state.toLowerCase();
  const attrUnit = entry.attributes?.unit_of_measurement;
  const unit = typeof attrUnit === 'string' ? attrUnit : (binding.unit ?? '');
  const active = ['on', 'open', 'opening', 'active', 'running', 'printing'].includes(st);
  const withUnit = binding.metricType === 'temperature' || binding.metricType === 'humidity' || binding.metricType === 'power';
  return { key: binding.id, entityId: binding.entityId, label: `${binding.label}: ${withUnit ? `${entry.state}${unit}` : entry.state}`, active };
}

function selectActiveFloor(layoutSpec: FloorplanLayoutSpec, demoSpec: FloorplanLayoutSpec, selectedFloorId?: string): FloorSpec {
  const found =
    layoutSpec.floors.find((f) => f.id === selectedFloorId || String(f.level) === selectedFloorId) ??
    layoutSpec.floors.find((f) => f.id === layoutSpec.defaultFloorId) ??
    layoutSpec.floors[0] ??
    demoSpec.floors[0];
  if (!found) throw new Error('Floorplan layout must include at least one floor');
  return found;
}

function getFloorPresets(layoutSpec: FloorplanLayoutSpec, floorId: string): readonly CameraPresetSpec[] {
  const filtered = layoutSpec.cameraPresets.filter((p) => !p.floorId || p.floorId === floorId);
  return filtered.length > 0 ? filtered : layoutSpec.cameraPresets;
}

function selectActivePreset(
  layoutSpec: FloorplanLayoutSpec,
  demoSpec: FloorplanLayoutSpec,
  floorPresets: readonly CameraPresetSpec[],
  selectedPresetId?: string,
): CameraPresetSpec {
  const found =
    floorPresets.find((p) => p.id === selectedPresetId) ??
    floorPresets.find((p) => p.id === layoutSpec.defaultPresetId) ??
    floorPresets[0] ??
    layoutSpec.cameraPresets[0] ??
    demoSpec.cameraPresets[0];
  if (!found) throw new Error('Floorplan layout must include at least one camera preset');
  return found;
}

interface WebglHandle {
  applyPreset: (preset: CameraPresetSpec, showUpper: boolean) => void;
  updateEntities: (states: FloorplanEntityMap) => void;
  dispose: () => void;
}

function mountWebglViewport(
  deps: FloorplanDeps,
  container: HTMLDivElement,
  canvas: HTMLCanvasElement,
  layout: FloorplanLayoutSpec,
  floor: FloorSpec,
  onSelectTarget: (hit: { roomId?: string; fixtureId?: string }) => void,
  onFail: () => void,
): WebglHandle | null {
  const { ACESFilmicToneMapping, PCFShadowMap, PerspectiveCamera, Raycaster, SRGBColorSpace, Vector2, Vector3, WebGLRenderer } = deps.three;
  let renderer: InstanceType<typeof WebGLRenderer>;
  try {
    renderer = new WebGLRenderer({ canvas, antialias: true, preserveDrawingBuffer: true, powerPreference: 'high-performance' });
  } catch {
    queueMicrotask(onFail);
    return null;
  }
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.outputColorSpace = SRGBColorSpace;
  renderer.toneMapping = ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.15;
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = PCFShadowMap;

  const built = deps.scene.buildFloorplanScene({ layout, floor, background: '#141922' });
  const camera = new PerspectiveCamera(40, 16 / 10, 0.25, 220);
  const orbit = { target: new Vector3(0, 2, 0), radius: 36, phi: 0.9, theta: 0 };
  const renderNow = () => { renderer.render(built.scene, camera); };
  const syncSize = () => {
    const w = Math.max(1, container.clientWidth || window.innerWidth || 800);
    const h = Math.max(1, container.clientHeight || window.innerHeight || 500);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    renderer.setSize(w, h, false);
    renderNow();
  };
  const applyPreset = (spec: CameraPresetSpec, showUpper: boolean) => {
    camera.fov = spec.fov;
    camera.position.set(...spec.position);
    camera.up.set(...spec.up);
    orbit.target.set(...spec.target);
    const off = camera.position.clone().sub(orbit.target);
    orbit.radius = Math.max(6, off.length());
    orbit.phi = Math.acos(clamp(off.y / orbit.radius, -1, 1));
    orbit.theta = Math.atan2(off.x, off.z);
    camera.lookAt(orbit.target);
    camera.updateProjectionMatrix();
    camera.updateMatrixWorld();
    built.setUpperFloorsVisible(showUpper);
    renderNow();
  };

  const pointers = new Map<number, { x: number; y: number }>();
  const gesture = { startX: 0, startY: 0, moved: false, pinchDist: 0 };
  const raycaster = new Raycaster();
  const zoomBy = (factor: number) => {
    orbit.radius = clamp(orbit.radius * factor, 8, 96);
    const dir = camera.position.clone().sub(orbit.target);
    if (dir.lengthSq() < 1e-8) dir.set(0, 0.5, 1);
    camera.position.copy(orbit.target).addScaledVector(dir.normalize(), orbit.radius);
    camera.lookAt(orbit.target);
    renderNow();
  };
  const capturePointer = (pointerId: number, capture: boolean) => {
    try {
      const c = canvas as { setPointerCapture?: (id: number) => void; releasePointerCapture?: (id: number) => void };
      if (capture) c.setPointerCapture?.(pointerId);
      else c.releasePointerCapture?.(pointerId);
    } catch { /* ignore unsupported or synthetic pointer events */ }
  };
  const onPointerDown = (e: PointerEvent) => {
    capturePointer(e.pointerId, true);
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pointers.size === 1) {
      Object.assign(gesture, { startX: e.clientX, startY: e.clientY, moved: false });
    } else if (pointers.size === 2) {
      const [a, b] = Array.from(pointers.values());
      if (a && b) Object.assign(gesture, { pinchDist: Math.hypot(a.x - b.x, a.y - b.y), moved: true });
    }
  };
  const onPointerMove = (e: PointerEvent) => {
    const prev = pointers.get(e.pointerId);
    if (!prev) return;
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pointers.size === 2) {
      const [a, b] = Array.from(pointers.values());
      if (a && b) {
        const dist = Math.hypot(a.x - b.x, a.y - b.y);
        if (gesture.pinchDist > 4 && dist > 4) zoomBy(gesture.pinchDist / dist);
        gesture.pinchDist = dist;
      }
      return;
    }
    if (Math.hypot(e.clientX - gesture.startX, e.clientY - gesture.startY) > 4) gesture.moved = true;
    if (!gesture.moved) return;
    orbit.theta -= (e.clientX - prev.x) * 0.0065;
    orbit.phi = clamp(orbit.phi - (e.clientY - prev.y) * 0.0055, 0.08, Math.PI * 0.46);
    camera.up.set(0, 1, 0);
    camera.position.set(
      orbit.target.x + orbit.radius * Math.sin(orbit.phi) * Math.sin(orbit.theta),
      orbit.target.y + orbit.radius * Math.cos(orbit.phi),
      orbit.target.z + orbit.radius * Math.sin(orbit.phi) * Math.cos(orbit.theta),
    );
    camera.lookAt(orbit.target);
    renderNow();
  };
  const onPointerEnd = (e: PointerEvent) => {
    capturePointer(e.pointerId, false);
    pointers.delete(e.pointerId);
  };
  const onPointerUp = (e: PointerEvent) => {
    const wasTap = pointers.has(e.pointerId) && pointers.size === 1 && !gesture.moved;
    onPointerEnd(e);
    const rect = canvas.getBoundingClientRect();
    if (!wasTap || rect.width <= 0 || rect.height <= 0) return;
    raycaster.setFromCamera(
      new Vector2(((e.clientX - rect.left) / rect.width) * 2 - 1, -((e.clientY - rect.top) / rect.height) * 2 + 1),
      camera,
    );
    for (const hit of raycaster.intersectObjects(built.scene.children, true)) {
      for (let cur: Object3D | null = hit.object; cur; cur = cur.parent) {
        const u = cur.userData as { fixtureId?: string; roomId?: string; room?: string } | undefined;
        const fId = typeof u?.fixtureId === 'string' ? u.fixtureId : undefined;
        const rId = typeof u?.roomId === 'string' ? u.roomId : typeof u?.room === 'string' ? u.room : undefined;
        if (fId || rId) {
          onSelectTarget({ ...(rId !== undefined ? { roomId: rId } : {}), ...(fId !== undefined ? { fixtureId: fId } : {}) });
          return;
        }
      }
    }
  };
  const onWheel = (e: WheelEvent) => { e.preventDefault(); zoomBy(e.deltaY > 0 ? 1.08 : 0.92); };
  canvas.addEventListener('pointerdown', onPointerDown);
  canvas.addEventListener('pointermove', onPointerMove);
  canvas.addEventListener('pointerup', onPointerUp);
  canvas.addEventListener('pointercancel', onPointerEnd);
  canvas.addEventListener('lostpointercapture', onPointerEnd);
  canvas.addEventListener('wheel', onWheel, { passive: false });
  const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(syncSize) : null;
  ro?.observe(container);
  syncSize();
  window.__floorplanSceneReady = true;
  return {
    applyPreset,
    updateEntities: (states) => { built.updateEntities(states); renderNow(); },
    dispose: () => {
      delete window.__floorplanSceneReady;
      ro?.disconnect();
      canvas.removeEventListener('pointerdown', onPointerDown);
      canvas.removeEventListener('pointermove', onPointerMove);
      canvas.removeEventListener('pointerup', onPointerUp);
      canvas.removeEventListener('pointercancel', onPointerEnd);
      canvas.removeEventListener('lostpointercapture', onPointerEnd);
      canvas.removeEventListener('wheel', onWheel);
      built.dispose();
      renderer.dispose();
    },
  };
}

function Floorplan3dContent({ id, type, deps }: WidgetComponentProps & { deps: FloorplanDeps }) {
  const { DEFAULT_DEMO_FLOORPLAN_SPEC, parseFloorplanConfig } = deps.spec;
  const entry = useWidget(id);
  const metaConfig = entry.meta?.config;
  const layoutUrl = resolveLayoutUrl(metaConfig, entry.data);
  const [fetchedLayout, setFetchedLayout] = useState<{ url: string; spec: FloorplanLayoutSpec } | null>(null);
  useEffect(() => {
    if (!layoutUrl || typeof fetch !== 'function') return undefined;
    let cancelled = false;
    void fetch(layoutUrl)
      .then((res) => (res.ok ? res.json() : Promise.reject(new Error(`HTTP ${res.status}`))))
      .then((json: unknown) => { if (!cancelled) setFetchedLayout({ url: layoutUrl, spec: parseFloorplanConfig(json) }); })
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [layoutUrl, parseFloorplanConfig]);

  const hasMetaLayout = Boolean(metaConfig && ('floors' in metaConfig || 'layout' in metaConfig));
  const inlineLayoutKey = useMemo(
    () => serializeInlineLayout(hasMetaLayout, hasMetaLayout ? metaConfig : entry.data),
    [hasMetaLayout, metaConfig, entry.data],
  );
  const inlineSpec = useMemo(
    () => parseInlineLayoutKey(inlineLayoutKey, parseFloorplanConfig, DEFAULT_DEMO_FLOORPLAN_SPEC),
    [DEFAULT_DEMO_FLOORPLAN_SPEC, inlineLayoutKey, parseFloorplanConfig],
  );
  const layoutSpec = layoutUrl && fetchedLayout?.url === layoutUrl ? fetchedLayout.spec : inlineSpec;
  const [selectedFloorId, setSelectedFloorId] = useState<string | undefined>(() => readQueryParam('floor', 'tier'));
  const [selectedPresetId, setSelectedPresetId] = useState<string | undefined>(() => readQueryParam('floorplanPreset', 'view'));
  const [selectedFixtureId, setSelectedFixtureId] = useState<string | null>(null);
  const [showUpperFloors, setShowUpperFloors] = useState<boolean>(false);
  const [webglSupported, setWebglSupported] = useState<boolean>(() => canCreateWebgl(deps.scene.isUnmockedJsdomCanvas));
  const [isKiosk] = useState<boolean>(() => ['1', 'true'].includes(readQueryParam('kiosk') ?? ''));

  const activeFloor = useMemo(() => selectActiveFloor(layoutSpec, DEFAULT_DEMO_FLOORPLAN_SPEC, selectedFloorId), [DEFAULT_DEMO_FLOORPLAN_SPEC, layoutSpec, selectedFloorId]);
  const floorPresets = useMemo(() => getFloorPresets(layoutSpec, activeFloor.id), [layoutSpec, activeFloor.id]);
  const activePreset = useMemo(() => selectActivePreset(layoutSpec, DEFAULT_DEMO_FLOORPLAN_SPEC, floorPresets, selectedPresetId), [DEFAULT_DEMO_FLOORPLAN_SPEC, floorPresets, layoutSpec, selectedPresetId]);
  const selectedFixture = useMemo(() => activeFloor.fixtures.find((f) => f.id === selectedFixtureId) ?? null, [activeFloor.fixtures, selectedFixtureId]);
  const entities = useMemo(() => extractEntities(entry.data), [entry.data]);
  const hudPills = useMemo(() => {
    const floorRoomIds = new Set(activeFloor.rooms.map((r) => r.id));
    return layoutSpec.haBindings.filter((b) => !b.roomId || floorRoomIds.has(b.roomId)).map((b) => formatHudPill(b, readBindingState(entities, b)));
  }, [activeFloor.rooms, layoutSpec.haBindings, entities]);

  const viewportRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const handleRef = useRef<WebglHandle | null>(null);
  const switchFloor = useCallback((floorId: string) => {
    const nextFloor = selectActiveFloor(layoutSpec, DEFAULT_DEMO_FLOORPLAN_SPEC, floorId);
    const nextPresets = getFloorPresets(layoutSpec, nextFloor.id);
    setSelectedFloorId(floorId);
    setSelectedFixtureId(null);
    setSelectedPresetId((prev) => (prev && nextPresets.some((p) => p.id === prev) ? prev : undefined));
  }, [DEFAULT_DEMO_FLOORPLAN_SPEC, layoutSpec]);
  useEffect(() => {
    window.__setFloorplanPreset = (presetId: string) => { setSelectedPresetId(presetId); };
    window.__setFloorplanFloor = switchFloor;
    return () => { delete window.__setFloorplanPreset; delete window.__setFloorplanFloor; };
  }, [switchFloor]);
  useEffect(() => {
    if (!webglSupported || !viewportRef.current || !canvasRef.current) return undefined;
    const handle = mountWebglViewport(
      deps, viewportRef.current, canvasRef.current, layoutSpec, activeFloor,
      (hit) => {
        if (hit.fixtureId) setSelectedFixtureId(hit.fixtureId);
        if (hit.roomId && floorPresets.some((p) => p.id === hit.roomId)) setSelectedPresetId(hit.roomId);
      },
      () => { setWebglSupported(false); },
    );
    handleRef.current = handle;
    return () => { handle?.dispose(); handleRef.current = null; };
  }, [deps, webglSupported, layoutSpec, activeFloor, floorPresets]);
  useEffect(() => { handleRef.current?.applyPreset(activePreset, showUpperFloors); }, [activePreset, showUpperFloors, webglSupported, activeFloor]);
  useEffect(() => { handleRef.current?.updateEntities(entities); }, [entities, webglSupported, activeFloor]);
  const selectRoomTarget = (roomId: string) => { if (floorPresets.some((p) => p.id === roomId)) setSelectedPresetId(roomId); };

  return (
    <div
      className="fp3d-widget" data-widget-type={type} data-active-floor={activeFloor.id}
      data-active-preset={activePreset.id} data-upper-floors={showUpperFloors ? 'true' : 'false'} data-kiosk={isKiosk ? 'true' : 'false'}
    >
      <div className="fp3d-viewport" ref={viewportRef}>
        {webglSupported ? (
          <canvas className="fp3d-canvas" ref={canvasRef} aria-label={`${layoutSpec.title} — ${activeFloor.label} 3D Canvas`} />
        ) : (
          <div className="fp3d-fallback" role="region" aria-label={`${layoutSpec.title} — ${activeFloor.label} Overview`}>
            {activeFloor.rooms.map((room) => {
              const roomFixtures = activeFloor.fixtures.filter((f) => f.roomId === room.id);
              return (
                <div key={room.id} className="fp3d-fallback-room" data-room-id={room.id}>
                  <button type="button" className="fp3d-fallback-room-btn" onClick={() => { selectRoomTarget(room.id); }}>
                    <strong>{room.label}</strong>
                    <div>{roomFixtures.length} fixtures · {activePreset.label}</div>
                  </button>
                  {roomFixtures.length > 0 && (
                    <div className="fp3d-fallback-fixtures">
                      {roomFixtures.map((fix) => (
                        <button
                          key={fix.id} type="button" className="fp3d-fixture-chip" data-fixture-id={fix.id}
                          data-selected={selectedFixture?.id === fix.id ? 'true' : 'false'}
                          onClick={() => { setSelectedFixtureId(fix.id); selectRoomTarget(room.id); }}
                        >{fix.label}</button>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
      <div className="fp3d-hud-top">
        <div className="fp3d-title-chip">{layoutSpec.title} · {activeFloor.label} · {activePreset.label}</div>
        <div className="fp3d-status-list">
          {hudPills.length > 0
            ? hudPills.map((p) => (
                <span key={p.key} className="fp3d-status-pill" data-status-key={p.key} data-entity-id={p.entityId} data-active={p.active ? 'true' : 'false'}>
                  <span className="fp3d-status-dot" />{p.label}
                </span>
              ))
            : activeFloor.rooms.map((room) => (
                <span key={room.id} className="fp3d-status-pill" data-room-badge={room.id} data-active="true">
                  <span className="fp3d-status-dot" />{room.label}
                </span>
              ))}
        </div>
      </div>
      {selectedFixture && (
        <div className="fp3d-inspector" data-testid="floorplan-fixture-inspector" data-selected-fixture={selectedFixture.id}>
          <div className="fp3d-inspector-header">
            <span>{selectedFixture.label}</span>
            <button type="button" className="fp3d-inspector-close" data-testid="floorplan-inspector-close" aria-label="Close fixture inspector" onClick={() => { setSelectedFixtureId(null); }}>×</button>
          </div>
          <div className="fp3d-inspector-meta">
            <div>Kind: {selectedFixture.kind} · Room: {selectedFixture.roomId}</div>
            <div>
              Size: {(selectedFixture.bounds.xMax - selectedFixture.bounds.xMin).toFixed(1)} ×{' '}
              {(selectedFixture.bounds.zMax - selectedFixture.bounds.zMin).toFixed(1)} ×{' '}
              {(selectedFixture.yMax - selectedFixture.yMin).toFixed(1)} ft
            </div>
          </div>
        </div>
      )}

      <div className="fp3d-controls" role="toolbar" aria-label="Floorplan Camera Controls">
        {layoutSpec.floors.map((floor) => (
          <button
            key={floor.id} type="button" className="fp3d-pill" data-testid={`floorplan-floor-${floor.id}-btn`}
            data-floor-id={floor.id} data-active={activeFloor.id === floor.id ? 'true' : 'false'}
            aria-pressed={activeFloor.id === floor.id} onClick={() => { switchFloor(floor.id); }}
          >{floor.label}</button>
        ))}
        <span className="fp3d-divider" aria-hidden="true" />
        {floorPresets.map((preset) => (
          <button
            key={preset.id} type="button" className="fp3d-pill" data-preset={preset.id}
            data-active={activePreset.id === preset.id ? 'true' : 'false'}
            aria-pressed={activePreset.id === preset.id} onClick={() => { setSelectedPresetId(preset.id); }}
          >{preset.label}</button>
        ))}
        <span className="fp3d-divider" aria-hidden="true" />
        <button type="button" className="fp3d-pill" data-toggle="upper-floors" aria-pressed={showUpperFloors} onClick={() => { setShowUpperFloors((prev) => !prev); }}>
          Upper Massing
        </button>
      </div>
    </div>
  );
}

export function Floorplan3dWidget(props: WidgetComponentProps) {
  const [deps, setDeps] = useState<FloorplanDeps | null>(null);
  useEffect(() => {
    let cancelled = false;
    void Promise.all([import('three'), import('./buildFloorplanScene'), import('./floorplanSpec')])
      .then(([three, scene, spec]) => { if (!cancelled) setDeps({ three, scene, spec }); });
    return () => { cancelled = true; };
  }, []);
  if (!deps) return <div className="fp3d-widget" data-widget-type={props.type} data-loading="true" />;
  return <Floorplan3dContent {...props} deps={deps} />;
}
