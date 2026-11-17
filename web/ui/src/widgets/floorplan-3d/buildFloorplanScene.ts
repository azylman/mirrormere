import {
  BoxGeometry, CanvasTexture, Color, DataTexture, DirectionalLight, Group,
  HemisphereLight, Mesh, MeshStandardMaterial, PointLight, RGBAFormat,
  RepeatWrapping, SRGBColorSpace, Scene, type BufferGeometry, type Texture,
} from 'three';
import { buildFloorFixtures } from './floorplanFixtures';
import {
  DEFAULT_DEMO_FLOORPLAN_SPEC, type Bounds2D, type FloorMaterialType,
  type FloorSpec, type FloorplanLayoutSpec, type FloorplanWallSpec, type HaEntityBindingSpec,
} from './floorplanSpec';

export type FloorplanFloorSpec = FloorSpec;

export interface FloorplanEntityStateLike {
  state: string;
  attributes?: Record<string, unknown>;
}

export type FloorplanEntityMap = Readonly<
  Record<string, FloorplanEntityStateLike | string | undefined>
>;

export interface FloorplanSceneLights {
  hemisphere: HemisphereLight;
  sun: DirectionalLight;
  roomPoints: Readonly<Record<string, PointLight>>;
}

export interface BuildFloorplanSceneOptions {
  floor?: FloorplanFloorSpec;
  layout?: FloorplanLayoutSpec;
  floorId?: string;
  showUpperFloors?: boolean;
  background?: string | null;
  haBindings?: readonly HaEntityBindingSpec[];
  initialEntities?: FloorplanEntityMap;
}

export interface FloorplanSceneBuildResult {
  scene: Scene;
  floor: FloorplanFloorSpec;
  root: Group;
  rooms: Readonly<Record<string, Group>>;
  roomFloors: Readonly<Record<string, Mesh>>;
  wallsById: Readonly<Record<string, Group>>;
  fixtures: Readonly<Record<string, Group>>;
  lights: FloorplanSceneLights;
  upperFloorsGroup: Group;
  setUpperFloorsVisible: (visible: boolean) => void;
  updateEntities: (states: FloorplanEntityMap) => void;
  dispose: () => void;
}

type AddBoxFn = (
  parent: Group, x0: number, x1: number, y0: number, y1: number, z0: number, z1: number,
  mat: MeshStandardMaterial, name?: string,
) => Mesh;

export function isUnmockedJsdomCanvas(): boolean {
  if (!/jsdom/i.test(navigator.userAgent)) return false;
  const fn: unknown = Reflect.get(HTMLCanvasElement.prototype, 'getContext');
  return typeof fn !== 'function' || !('mock' in fn);
}

function drawFloorPattern(
  ctx: CanvasRenderingContext2D, w: number, h: number, kind: FloorMaterialType, baseHex: string,
): void {
  ctx.fillStyle = baseHex;
  ctx.fillRect(0, 0, w, h);
  ctx.lineWidth = 1;
  if (kind === 'wood') {
    const rows = 8;
    const rowH = h / rows;
    ctx.strokeStyle = 'rgba(55, 32, 14, 0.24)';
    for (let r = 0; r < rows; r += 1) {
      const y = r * rowH;
      const jointX = ((r * 37 + 19) % 70) * (w / 80);
      ctx.beginPath();
      ctx.moveTo(0, y + 0.5);
      ctx.lineTo(w, y + 0.5);
      ctx.moveTo(jointX, y);
      ctx.lineTo(jointX, y + rowH);
      ctx.stroke();
    }
    return;
  }
  if (kind === 'tile' || kind === 'stone') {
    const step = w / 4;
    ctx.strokeStyle = 'rgba(40, 46, 54, 0.22)';
    for (let i = 0; i <= 4; i += 1) {
      const p = i * step;
      ctx.beginPath();
      ctx.moveTo(p, 0);
      ctx.lineTo(p, h);
      ctx.moveTo(0, p);
      ctx.lineTo(w, p);
      ctx.stroke();
    }
    return;
  }
  ctx.fillStyle = 'rgba(255, 255, 255, 0.05)';
  ctx.fillRect(0, 0, w / 2, h / 2);
  ctx.fillRect(w / 2, h / 2, w / 2, h / 2);
}

function makeFloorTexture(kind: FloorMaterialType, hex: string): Texture {
  let ctx: CanvasRenderingContext2D | null = null;
  let canvas: HTMLCanvasElement | null = null;
  if (!isUnmockedJsdomCanvas()) {
    try {
      canvas = document.createElement('canvas');
      canvas.width = 128;
      canvas.height = 128;
      ctx = canvas.getContext('2d');
    } catch {
      ctx = null;
    }
  }
  let tex: Texture;
  if (ctx && canvas) {
    drawFloorPattern(ctx, 128, 128, kind, hex);
    tex = new CanvasTexture(canvas);
  } else {
    const c = new Color(hex);
    const rgba = [Math.round(c.r * 255), Math.round(c.g * 255), Math.round(c.b * 255), 255];
    tex = new DataTexture(new Uint8Array(rgba), 1, 1, RGBAFormat);
  }
  tex.colorSpace = SRGBColorSpace;
  tex.wrapS = RepeatWrapping;
  tex.wrapT = RepeatWrapping;
  tex.repeat.set(3, 3);
  tex.needsUpdate = true;
  return tex;
}

function applyWorldFloorUVs(geom: BufferGeometry, bounds: Bounds2D): void {
  const pos = geom.attributes.position;
  const uv = geom.attributes.uv;
  if (!pos || !uv) return;
  const [cx, cz] = [(bounds.xMin + bounds.xMax) / 2, (bounds.zMin + bounds.zMax) / 2];
  for (let i = 0; i < pos.count; i += 1) {
    uv.setXY(i, (cx + pos.getX(i)) / 10, (cz + pos.getZ(i)) / 10);
  }
  uv.needsUpdate = true;
}

function buildWallGroup(
  wall: FloorplanWallSpec,
  wallMat: MeshStandardMaterial,
  trimMat: MeshStandardMaterial,
  glassMat: MeshStandardMaterial,
  addBox: AddBoxFn,
): Group {
  const wg = new Group();
  wg.name = `wall-${wall.id}`;
  wg.userData = { wallId: wall.id };

  const [dx, dz] = [wall.x2 - wall.x1, wall.z2 - wall.z1];
  const isHorizontal = Math.abs(dx) >= Math.abs(dz);
  const length = Math.max(0.1, isHorizontal ? Math.abs(dx) : Math.abs(dz));
  const halfT = Math.max(0.05, wall.thickness / 2);

  const rawStops = new Set<number>([0, length]);
  for (const op of wall.openings) {
    rawStops.add(Math.max(0, Math.min(length, Math.min(op.offsetStart, op.offsetEnd))));
    rawStops.add(Math.max(0, Math.min(length, Math.max(op.offsetStart, op.offsetEnd))));
  }
  const stops = Array.from(rawStops).sort((a, b) => a - b);
  const heightAt = (t: number): number => wall.heightStart + (wall.heightEnd - wall.heightStart) * (t / length);

  const placeSpan = (t0: number, t1: number, y0: number, y1: number, mat: MeshStandardMaterial, suffix: string): void => {
    if (t1 - t0 < 1e-3 || y1 - y0 < 1e-3) return;
    if (isHorizontal) {
      const [xStart, zMid] = [Math.min(wall.x1, wall.x2), (wall.z1 + wall.z2) / 2];
      addBox(wg, xStart + t0, xStart + t1, y0, y1, zMid - halfT, zMid + halfT, mat, `${wall.id}-${suffix}`);
    } else {
      const [zStart, xMid] = [Math.min(wall.z1, wall.z2), (wall.x1 + wall.x2) / 2];
      addBox(wg, xMid - halfT, xMid + halfT, y0, y1, zStart + t0, zStart + t1, mat, `${wall.id}-${suffix}`);
    }
  };

  for (let i = 0; i < stops.length - 1; i += 1) {
    const [t0, t1] = [stops[i] ?? 0, stops[i + 1] ?? length];
    if (t1 - t0 < 1e-3) continue;
    const tMid = (t0 + t1) / 2;
    const hSeg = Math.max(0.2, (heightAt(t0) + heightAt(t1)) / 2);
    const capH = hSeg >= 1.0 ? 0.07 : 0;
    const bodyTop = hSeg - capH;
    const opening = wall.openings.find((op) => {
      const [s0, s1] = [Math.min(op.offsetStart, op.offsetEnd), Math.max(op.offsetStart, op.offsetEnd)];
      return tMid > s0 && tMid < s1;
    });

    if (!opening) {
      placeSpan(t0, t1, 0, bodyTop, wallMat, `body-${i}`);
      if (capH > 0) placeSpan(t0, t1, bodyTop, hSeg, trimMat, `cap-${i}`);
      continue;
    }
    if (opening.yMin > 0.02) placeSpan(t0, t1, 0, Math.min(bodyTop, opening.yMin), wallMat, `sill-${i}`);
    if (opening.yMax < bodyTop - 0.02) {
      placeSpan(t0, t1, opening.yMax, bodyTop, wallMat, `header-${i}`);
      if (capH > 0) placeSpan(t0, t1, bodyTop, hSeg, trimMat, `cap-${i}`);
    }
    if (opening.type === 'window' && opening.yMax > opening.yMin) {
      placeSpan(t0, t1, opening.yMin, Math.min(hSeg, opening.yMax), glassMat, `glass-${i}`);
    }
  }
  return wg;
}

function resolveEntityEntry(states: FloorplanEntityMap, binding: HaEntityBindingSpec): FloorplanEntityStateLike | undefined {
  const raw = states[binding.entityId] ?? states[binding.id];
  return raw === undefined ? undefined : typeof raw === 'string' ? { state: raw } : raw;
}

function isEntityActive(entry: FloorplanEntityStateLike, metricType: HaEntityBindingSpec['metricType']): boolean {
  const st = entry.state.toLowerCase();
  if (metricType === 'power') {
    const num = Number.parseFloat(entry.state);
    return Number.isFinite(num) ? num > 5 : st === 'on' || st === 'active';
  }
  return st === 'on' || st === 'open' || st === 'opening' || st === 'active' || st === 'running' || st === 'printing' || st === 'heating';
}

function resolveTargetFloor(layout: FloorplanLayoutSpec, floorId?: string, override?: FloorplanFloorSpec): FloorplanFloorSpec {
  if (override) return override;
  const found =
    layout.floors.find((f) => f.id === (floorId ?? layout.defaultFloorId)) ??
    layout.floors[0] ??
    DEFAULT_DEMO_FLOORPLAN_SPEC.floors[0];
  if (!found) throw new Error('Default floorplan specification must define at least one floor');
  return found;
}

export function buildFloorplanScene(options: BuildFloorplanSceneOptions = {}): FloorplanSceneBuildResult {
  const layout = options.layout ?? DEFAULT_DEMO_FLOORPLAN_SPEC;
  const floor = resolveTargetFloor(layout, options.floorId, options.floor);
  const haBindings = options.haBindings ?? layout.haBindings;

  const geometries: BufferGeometry[] = [];
  const materials: MeshStandardMaterial[] = [];
  const textures: Texture[] = [];
  const wallMatCache = new Map<string, MeshStandardMaterial>();

  const trackMat = (opts: ConstructorParameters<typeof MeshStandardMaterial>[0]): MeshStandardMaterial => {
    const m = new MeshStandardMaterial(opts);
    materials.push(m);
    return m;
  };
  const getWallMat = (color: string): MeshStandardMaterial => {
    const cached = wallMatCache.get(color);
    if (cached) return cached;
    const created = trackMat({ color, roughness: 0.84, metalness: 0.04 });
    wallMatCache.set(color, created);
    return created;
  };

  const trimMat = trackMat({ color: '#f8fafc', roughness: 0.5, metalness: 0.05 });
  const glassMat = trackMat({ color: '#9ac7e3', transparent: true, opacity: 0.36, roughness: 0.14, metalness: 0.1 });
  const plinthMat = trackMat({ color: '#2b313b', roughness: 0.82, metalness: 0.06 });
  const upperMassMat = trackMat({ color: '#64748b', transparent: true, opacity: 0.32, roughness: 0.72 });

  const addBox: AddBoxFn = (parent, x0, x1, y0, y1, z0, z1, mat, name) => {
    const geom = new BoxGeometry(Math.max(0.02, x1 - x0), Math.max(0.02, y1 - y0), Math.max(0.02, z1 - z0));
    geometries.push(geom);
    const mesh = new Mesh(geom, mat);
    if (name) mesh.name = name;
    mesh.position.set((x0 + x1) / 2, (y0 + y1) / 2, (z0 + z1) / 2);
    mesh.castShadow = true;
    mesh.receiveShadow = true;
    parent.add(mesh);
    return mesh;
  };

  const root = new Group();
  root.name = `floorplan-root-${floor.id}`;
  addBox(
    root, floor.bounds.xMin - 0.4, floor.bounds.xMax + 0.4, -0.36, -0.16,
    floor.bounds.zMin - 0.4, floor.bounds.zMax + 0.4, plinthMat, `foundation-${floor.id}`,
  );

  const rooms: Record<string, Group> = {};
  const roomFloors: Record<string, Mesh> = {};
  const roomPoints: Record<string, PointLight> = {};
  const lightsGroup = new Group();
  lightsGroup.name = `lights-${floor.id}`;

  for (const rSpec of floor.rooms) {
    const rGroup = new Group();
    rGroup.name = `room-${rSpec.id}`;
    rGroup.userData = { roomId: rSpec.id, room: rSpec.id, label: rSpec.label };

    const tex = makeFloorTexture(rSpec.floorMaterial, rSpec.floorColor);
    textures.push(tex);
    const roughness = rSpec.floorMaterial === 'carpet' ? 0.92 : rSpec.floorMaterial === 'tile' ? 0.48 : 0.62;
    const floorMat = trackMat({ map: tex, roughness, metalness: 0.04 });
    const slab = addBox(
      rGroup, rSpec.bounds.xMin, rSpec.bounds.xMax, rSpec.yMin - 0.16, rSpec.yMin,
      rSpec.bounds.zMin, rSpec.bounds.zMax, floorMat, `floor-${rSpec.id}`,
    );
    slab.userData = { roomId: rSpec.id, room: rSpec.id, label: rSpec.label, floorMaterial: rSpec.floorMaterial };
    applyWorldFloorUVs(slab.geometry, rSpec.bounds);

    const pt = new PointLight('#ffe9c8', 0.85, 28, 1.4);
    pt.position.set(
      (rSpec.bounds.xMin + rSpec.bounds.xMax) / 2,
      Math.max(2.5, rSpec.yMax - 0.8),
      (rSpec.bounds.zMin + rSpec.bounds.zMax) / 2,
    );
    lightsGroup.add(pt);
    roomPoints[rSpec.id] = pt;
    rooms[rSpec.id] = rGroup;
    roomFloors[rSpec.id] = slab;
    root.add(rGroup);
  }

  const wallsGroup = new Group();
  wallsGroup.name = `walls-${floor.id}`;
  const wallsById: Record<string, Group> = {};
  for (const wall of floor.walls) {
    const wg = buildWallGroup(wall, getWallMat(wall.color), trimMat, glassMat, addBox);
    wallsById[wall.id] = wg;
    wallsGroup.add(wg);
  }
  root.add(wallsGroup);

  const fixturesBuilt = buildFloorFixtures(floor);
  root.add(fixturesBuilt.root);

  const upperFloorsGroup = new Group();
  upperFloorsGroup.name = 'upper-floors-massing';
  upperFloorsGroup.visible = Boolean(options.showUpperFloors);
  const otherFloors = layout.floors.filter((f) => f.id !== floor.id);
  if (otherFloors.length > 0) {
    for (const other of otherFloors) {
      const yBase = other.elevationY > floor.elevationY ? other.elevationY : floor.ceilingHeight + 0.5;
      addBox(
        upperFloorsGroup, other.bounds.xMin, other.bounds.xMax, yBase, yBase + other.ceilingHeight * 0.45,
        other.bounds.zMin, other.bounds.zMax, upperMassMat, `massing-${other.id}`,
      );
    }
  } else {
    addBox(
      upperFloorsGroup, floor.bounds.xMin, floor.bounds.xMax, floor.ceilingHeight + 0.4, floor.ceilingHeight + 4.2,
      floor.bounds.zMin, floor.bounds.zMax, upperMassMat, `massing-upper-${floor.id}`,
    );
  }
  root.add(upperFloorsGroup);

  const hemisphere = new HemisphereLight('#fffaf0', '#47505c', 0.95);
  hemisphere.position.set(0, 24, 0);
  const sun = new DirectionalLight('#fff6e8', 1.35);
  sun.position.set(12, 28, 18);
  sun.castShadow = true;
  sun.shadow.mapSize.set(1024, 1024);
  lightsGroup.add(hemisphere, sun, sun.target);
  root.add(lightsGroup);

  const scene = new Scene();
  scene.name = `floorplan-scene-${floor.id}`;
  scene.background = options.background === null ? null : new Color(options.background ?? '#13171e');
  scene.add(root);
  scene.updateMatrixWorld(true);

  const updateEntities = (states: FloorplanEntityMap): void => {
    for (const binding of haBindings) {
      const entry = resolveEntityEntry(states, binding);
      if (!entry) continue;
      const active = isEntityActive(entry, binding.metricType);
      const roomSlab = binding.roomId ? roomFloors[binding.roomId] : undefined;
      const roomLight = binding.roomId ? roomPoints[binding.roomId] : undefined;
      const fixtureGroup = binding.fixtureId ? fixturesBuilt.fixtures[binding.fixtureId] : undefined;
      const statusMat = binding.fixtureId ? fixturesBuilt.statusMaterials[binding.fixtureId] : undefined;

      if (binding.metricType === 'light' && roomLight) {
        const rawBri = entry.attributes?.brightness;
        const bri = typeof rawBri === 'number' ? Math.max(0.1, Math.min(1, rawBri / 255)) : 1;
        roomLight.intensity = active ? Number((1.25 * bri).toFixed(3)) : 0;
        if (roomSlab) roomSlab.userData.lightState = active ? 'on' : 'off';
      }
      if (binding.metricType === 'temperature' || binding.metricType === 'humidity' || binding.metricType === 'power') {
        const attrUnit = entry.attributes?.unit_of_measurement;
        const unit = typeof attrUnit === 'string' ? attrUnit : (binding.unit ?? '');
        const formatted = `${entry.state}${unit}`;
        if (roomSlab) roomSlab.userData[binding.metricType] = formatted;
        if (fixtureGroup) fixtureGroup.userData[binding.metricType] = formatted;
      }
      if (fixtureGroup) {
        fixtureGroup.userData.status = entry.state.toLowerCase();
        fixtureGroup.userData.active = active;
      }
      if (statusMat) {
        statusMat.color.set(active ? '#38bdf8' : '#475569');
        statusMat.emissive.set(active ? '#0284c7' : '#000000');
        statusMat.emissiveIntensity = active ? 0.85 : 0.05;
      }
    }
  };

  if (options.initialEntities) updateEntities(options.initialEntities);

  return {
    scene, floor, root, rooms, roomFloors, wallsById,
    fixtures: fixturesBuilt.fixtures,
    lights: { hemisphere, sun, roomPoints },
    upperFloorsGroup,
    setUpperFloorsVisible: (visible: boolean) => { upperFloorsGroup.visible = visible; },
    updateEntities,
    dispose: () => {
      for (const g of geometries) g.dispose();
      for (const m of materials) m.dispose();
      for (const t of textures) t.dispose();
      fixturesBuilt.dispose();
    },
  };
}
