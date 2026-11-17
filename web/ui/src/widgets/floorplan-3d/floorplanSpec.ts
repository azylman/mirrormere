import { Box3, Vector3 } from 'three';

export type FacingDirection = '+X' | '-X' | '+Z' | '-Z' | 'up';
export type FloorMaterialType = 'wood' | 'tile' | 'concrete' | 'carpet' | 'stone';
export type WallOpeningType = 'door' | 'window' | 'archway';
export type FixtureKind =
  | 'desk' | 'bed' | 'sofa' | 'table' | 'chair' | 'cabinet' | 'bookcase'
  | 'rug' | 'appliance' | 'tv' | 'sink' | 'shower' | 'toilet' | 'stairs'
  | 'plant' | 'lamp' | 'art' | 'door' | 'custom';
export type HaMetricType = 'light' | 'temperature' | 'humidity' | 'power' | 'binary_state' | 'cover' | 'state';
export type Vec3Tuple = readonly [number, number, number];

export interface Bounds2D {
  xMin: number;
  xMax: number;
  zMin: number;
  zMax: number;
}

export interface FloorplanRoomSpec {
  id: string;
  label: string;
  bounds: Bounds2D;
  yMin: number;
  yMax: number;
  floorMaterial: FloorMaterialType;
  floorColor: string;
  wallColor: string;
}

export interface WallOpeningSpec {
  id: string;
  type: WallOpeningType;
  offsetStart: number;
  offsetEnd: number;
  yMin: number;
  yMax: number;
}

export interface FloorplanWallSpec {
  id: string;
  x1: number;
  z1: number;
  x2: number;
  z2: number;
  heightStart: number;
  heightEnd: number;
  thickness: number;
  color: string;
  openings: readonly WallOpeningSpec[];
}

export interface FloorplanFixtureSpec {
  id: string;
  roomId: string;
  label: string;
  kind: FixtureKind;
  facing: FacingDirection;
  bounds: Bounds2D;
  yMin: number;
  yMax: number;
  primaryColor: string;
  accentColor?: string;
  solidFootprint: boolean;
}

export interface CameraPresetSpec {
  id: string;
  label: string;
  position: Vec3Tuple;
  target: Vec3Tuple;
  up: Vec3Tuple;
  fov: number;
  floorId?: string;
}

export interface HaEntityBindingSpec {
  id: string;
  entityId: string;
  metricType: HaMetricType;
  label: string;
  roomId?: string;
  fixtureId?: string;
  unit?: string;
  interactive: boolean;
}

export interface FloorSpec {
  id: string;
  label: string;
  level: number;
  elevationY: number;
  ceilingHeight: number;
  wallThickness: number;
  bounds: Bounds2D;
  rooms: readonly FloorplanRoomSpec[];
  walls: readonly FloorplanWallSpec[];
  fixtures: readonly FloorplanFixtureSpec[];
}

export interface FloorplanLayoutSpec {
  title: string;
  defaultFloorId: string;
  defaultPresetId: string;
  floors: readonly FloorSpec[];
  cameraPresets: readonly CameraPresetSpec[];
  haBindings: readonly HaEntityBindingSpec[];
}

export const VALID_FACING_DIRECTIONS: readonly FacingDirection[] = ['+X', '-X', '+Z', '-Z', 'up'];
export const VALID_FLOOR_MATERIALS: readonly FloorMaterialType[] = ['wood', 'tile', 'concrete', 'carpet', 'stone'];
export const VALID_FIXTURE_KINDS: readonly FixtureKind[] = [
  'desk', 'bed', 'sofa', 'table', 'chair', 'cabinet', 'bookcase', 'rug', 'appliance',
  'tv', 'sink', 'shower', 'toilet', 'stairs', 'plant', 'lamp', 'art', 'door', 'custom',
];
export const VALID_HA_METRIC_TYPES: readonly HaMetricType[] = [
  'light', 'temperature', 'humidity', 'power', 'binary_state', 'cover', 'state',
];

export const DEFAULT_DEMO_FLOORPLAN_SPEC: FloorplanLayoutSpec = {
  title: 'Demo Studio Floorplan',
  defaultFloorId: 'floor-1',
  defaultPresetId: 'dollhouse',
  floors: [
    {
      id: 'floor-1', label: 'Floor 1', level: 1, elevationY: 0, ceilingHeight: 8.5, wallThickness: 0.4,
      bounds: { xMin: -12, xMax: 12, zMin: -8, zMax: 8 },
      rooms: [
        {
          id: 'studio', label: 'Studio', bounds: { xMin: -12, xMax: 4, zMin: -8, zMax: 8 },
          yMin: 0, yMax: 8.5, floorMaterial: 'wood', floorColor: '#9e6b3b', wallColor: '#f3efe6',
        },
        {
          id: 'entryway', label: 'Entryway', bounds: { xMin: 4, xMax: 12, zMin: -8, zMax: 8 },
          yMin: 0, yMax: 8.5, floorMaterial: 'tile', floorColor: '#8d969f', wallColor: '#e6eaee',
        },
      ],
      walls: [
        {
          id: 'wall-north', x1: -12, z1: -8, x2: 12, z2: -8, heightStart: 8.5, heightEnd: 8.5, thickness: 0.4, color: '#e9e5dc',
          openings: [{ id: 'north-window', type: 'window', offsetStart: 4, offsetEnd: 10, yMin: 2.5, yMax: 6.5 }],
        },
        { id: 'wall-west', x1: -12, z1: -8, x2: -12, z2: 8, heightStart: 8.5, heightEnd: 8.5, thickness: 0.4, color: '#e9e5dc', openings: [] },
        {
          id: 'wall-east', x1: 12, z1: -8, x2: 12, z2: 8, heightStart: 8.5, heightEnd: 8.5, thickness: 0.4, color: '#dde2e7',
          openings: [{ id: 'entry-door', type: 'door', offsetStart: 6, offsetEnd: 9, yMin: 0, yMax: 6.8 }],
        },
        { id: 'wall-south-cutaway', x1: -12, z1: 8, x2: 12, z2: 8, heightStart: 1.8, heightEnd: 1.8, thickness: 0.4, color: '#e9e5dc', openings: [] },
        {
          id: 'wall-divider', x1: 4, z1: -8, x2: 4, z2: 8, heightStart: 8.5, heightEnd: 4.5, thickness: 0.35, color: '#d8dde3',
          openings: [{ id: 'studio-entry-doorway', type: 'door', offsetStart: 9, offsetEnd: 12, yMin: 0, yMax: 6.8 }],
        },
      ],
      fixtures: [
        {
          id: 'studio-desk', roomId: 'studio', label: 'Work Desk', kind: 'desk', facing: '+Z',
          bounds: { xMin: -10.5, xMax: -5.5, zMin: -7.2, zMax: -4.8 }, yMin: 0, yMax: 2.5,
          primaryColor: '#7b5333', accentColor: '#24272c', solidFootprint: true,
        },
        {
          id: 'studio-chair', roomId: 'studio', label: 'Task Chair', kind: 'chair', facing: '-Z',
          bounds: { xMin: -8.9, xMax: -7.1, zMin: -4.4, zMax: -2.6 }, yMin: 0, yMax: 3.2,
          primaryColor: '#252930', solidFootprint: true,
        },
        {
          id: 'studio-sofa', roomId: 'studio', label: 'Lounge Sofa', kind: 'sofa', facing: '-Z',
          bounds: { xMin: -9.5, xMax: -2.5, zMin: 3.8, zMax: 6.8 }, yMin: 0, yMax: 2.9,
          primaryColor: '#3a4659', accentColor: '#d9d4c7', solidFootprint: true,
        },
        {
          id: 'studio-rug', roomId: 'studio', label: 'Area Rug', kind: 'rug', facing: 'up',
          bounds: { xMin: -10.0, xMax: -1.5, zMin: -1.5, zMax: 6.5 }, yMin: 0, yMax: 0.04,
          primaryColor: '#2c3e55', accentColor: '#e8e2d5', solidFootprint: false,
        },
        {
          id: 'entry-cabinet', roomId: 'entryway', label: 'Storage Cabinet', kind: 'cabinet', facing: '+Z',
          bounds: { xMin: 5.2, xMax: 10.6, zMin: -7.4, zMax: -5.6 }, yMin: 0, yMax: 3.1,
          primaryColor: '#6d737b', accentColor: '#cfd5db', solidFootprint: true,
        },
      ],
    },
  ],
  cameraPresets: [
    { id: 'dollhouse', label: 'Overview', position: [0, 22, 26], target: [0, 2, 0], up: [0, 1, 0], fov: 40, floorId: 'floor-1' },
    { id: 'studio', label: 'Studio', position: [-4, 15, 19], target: [-4, 2, 0], up: [0, 1, 0], fov: 42, floorId: 'floor-1' },
    { id: 'entryway', label: 'Entryway', position: [8, 14, 18], target: [8, 2, 0], up: [0, 1, 0], fov: 40, floorId: 'floor-1' },
    { id: 'topdown', label: 'Top-Down', position: [0, 38, 0.01], target: [0, 0, 0], up: [0, 0, -1], fov: 38, floorId: 'floor-1' },
  ],
  haBindings: [
    { id: 'studio-light', entityId: 'light.studio_main', metricType: 'light', label: 'Studio Light', roomId: 'studio', interactive: true },
    { id: 'studio-temp', entityId: 'sensor.studio_temperature', metricType: 'temperature', label: 'Studio Temp', roomId: 'studio', unit: '°F', interactive: false },
    { id: 'entry-light', entityId: 'light.entryway_main', metricType: 'light', label: 'Entry Light', roomId: 'entryway', interactive: true },
    { id: 'entry-door', entityId: 'binary_sensor.entry_door', metricType: 'binary_state', label: 'Entry Door', roomId: 'entryway', fixtureId: 'entry-cabinet', interactive: false },
  ],
};

type ObjRecord = Record<string, unknown>;
const asRecord = (v: unknown): ObjRecord | null => (v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as ObjRecord) : null);
const asNum = (v: unknown, fb: number): number => (typeof v === 'number' && Number.isFinite(v) ? v : fb);
const asStr = (v: unknown, fb: string): string => (typeof v === 'string' && v.trim().length > 0 ? v.trim() : fb);
const optStr = (v: unknown): string | undefined => (typeof v === 'string' && v.trim().length > 0 ? v.trim() : undefined);

function parseVec3(val: unknown, fallback: Vec3Tuple): Vec3Tuple {
  return Array.isArray(val) && val.length >= 3
    ? [asNum(val[0], fallback[0]), asNum(val[1], fallback[1]), asNum(val[2], fallback[2])]
    : fallback;
}

function parseBounds2D(rec: ObjRecord, fallback: Bounds2D): Bounds2D {
  const b = asRecord(rec.bounds) ?? rec;
  const [x0, x1] = [asNum(b.xMin, fallback.xMin), asNum(b.xMax, fallback.xMax)];
  const [z0, z1] = [asNum(b.zMin, fallback.zMin), asNum(b.zMax, fallback.zMax)];
  return { xMin: Math.min(x0, x1), xMax: Math.max(x0, x1), zMin: Math.min(z0, z1), zMax: Math.max(z0, z1) };
}

function parseRoom(raw: unknown, idx: number, ceilingHeight: number): FloorplanRoomSpec | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const id = asStr(rec.id, `room-${idx + 1}`);
  const matRaw = asStr(rec.floorMaterial, 'wood') as FloorMaterialType;
  return {
    id, label: asStr(rec.label, id),
    bounds: parseBounds2D(rec, { xMin: -6, xMax: 6, zMin: -6, zMax: 6 }),
    yMin: asNum(rec.yMin, 0), yMax: asNum(rec.yMax, ceilingHeight),
    floorMaterial: VALID_FLOOR_MATERIALS.includes(matRaw) ? matRaw : 'wood',
    floorColor: asStr(rec.floorColor, '#9e6b3b'), wallColor: asStr(rec.wallColor, '#f3efe6'),
  };
}

function parseWallOpening(raw: unknown, idx: number): WallOpeningSpec | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const rawType = asStr(rec.type, 'door');
  const type: WallOpeningType = rawType === 'window' || rawType === 'archway' ? rawType : 'door';
  return {
    id: asStr(rec.id, `opening-${idx + 1}`), type,
    offsetStart: asNum(rec.offsetStart, 0), offsetEnd: asNum(rec.offsetEnd, 3),
    yMin: asNum(rec.yMin, type === 'window' ? 2.5 : 0), yMax: asNum(rec.yMax, 6.8),
  };
}

function parseWall(raw: unknown, idx: number, ceilingHeight: number, defaultThickness: number): FloorplanWallSpec | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const hStart = asNum(rec.heightStart ?? rec.height, ceilingHeight);
  const openingsRaw = Array.isArray(rec.openings) ? rec.openings : [];
  return {
    id: asStr(rec.id, `wall-${idx + 1}`),
    x1: asNum(rec.x1 ?? rec.xMin, 0), z1: asNum(rec.z1 ?? rec.zMin, 0),
    x2: asNum(rec.x2 ?? rec.xMax, 4), z2: asNum(rec.z2 ?? rec.zMax, 0),
    heightStart: hStart, heightEnd: asNum(rec.heightEnd, hStart),
    thickness: asNum(rec.thickness, defaultThickness), color: asStr(rec.color, '#e9e5dc'),
    openings: openingsRaw.map((o, i) => parseWallOpening(o, i)).filter((o): o is WallOpeningSpec => o !== null),
  };
}

function parseFixture(raw: unknown, idx: number, defaultRoomId: string): FloorplanFixtureSpec | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const id = asStr(rec.id, `fixture-${idx + 1}`);
  const kindRaw = asStr(rec.kind ?? rec.category, 'custom') as FixtureKind;
  const kind: FixtureKind = VALID_FIXTURE_KINDS.includes(kindRaw) ? kindRaw : 'custom';
  const facingRaw = asStr(rec.facing, '+Z') as FacingDirection;
  const facing: FacingDirection = VALID_FACING_DIRECTIONS.includes(facingRaw) ? facingRaw : '+Z';
  let bounds = parseBounds2D(rec, { xMin: -1, xMax: 1, zMin: -1, zMax: 1 });
  let [yMin, yMax] = [asNum(rec.yMin, 0), asNum(rec.yMax, kind === 'rug' ? 0.04 : 2.8)];
  if (Array.isArray(rec.position) && Array.isArray(rec.size)) {
    const [pos, sz] = [parseVec3(rec.position, [0, 0, 0]), parseVec3(rec.size, [2, 2.5, 2])];
    bounds = { xMin: pos[0] - sz[0] / 2, xMax: pos[0] + sz[0] / 2, zMin: pos[2] - sz[2] / 2, zMax: pos[2] + sz[2] / 2 };
    [yMin, yMax] = [pos[1], pos[1] + sz[1]];
  }
  const accent = optStr(rec.accentColor);
  return {
    id, roomId: asStr(rec.roomId ?? rec.room, defaultRoomId), label: asStr(rec.label, id),
    kind, facing, bounds, yMin, yMax,
    primaryColor: asStr(rec.primaryColor ?? rec.color, '#7b5333'),
    ...(accent !== undefined ? { accentColor: accent } : {}),
    solidFootprint: typeof rec.solidFootprint === 'boolean' ? rec.solidFootprint : kind !== 'rug' && kind !== 'art' && kind !== 'door',
  };
}

function parseFloor(raw: unknown, idx: number): FloorSpec | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const [id, ceilingHeight, wallThickness] = [asStr(rec.id, `floor-${idx + 1}`), asNum(rec.ceilingHeight, 8.5), asNum(rec.wallThickness, 0.4)];
  const roomsRaw = Array.isArray(rec.rooms) ? rec.rooms : [];
  const rooms = roomsRaw.map((r, i) => parseRoom(r, i, ceilingHeight)).filter((r): r is FloorplanRoomSpec => r !== null);
  const firstRoom = rooms[0];
  if (!firstRoom) return null;
  const [wallsRaw, fixturesRaw] = [Array.isArray(rec.walls) ? rec.walls : [], Array.isArray(rec.fixtures) ? rec.fixtures : []];
  const computedBounds: Bounds2D = {
    xMin: Math.min(...rooms.map((r) => r.bounds.xMin)), xMax: Math.max(...rooms.map((r) => r.bounds.xMax)),
    zMin: Math.min(...rooms.map((r) => r.bounds.zMin)), zMax: Math.max(...rooms.map((r) => r.bounds.zMax)),
  };
  return {
    id, label: asStr(rec.label, `Floor ${idx + 1}`), level: asNum(rec.level, idx + 1),
    elevationY: asNum(rec.elevationY, idx * (ceilingHeight + 1)), ceilingHeight, wallThickness,
    bounds: asRecord(rec.bounds) ? parseBounds2D(rec, computedBounds) : computedBounds, rooms,
    walls: wallsRaw.map((w, i) => parseWall(w, i, ceilingHeight, wallThickness)).filter((w): w is FloorplanWallSpec => w !== null),
    fixtures: fixturesRaw.map((f, i) => parseFixture(f, i, firstRoom.id)).filter((f): f is FloorplanFixtureSpec => f !== null),
  };
}

function parseCameraPreset(raw: unknown, idx: number): CameraPresetSpec | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const [id, floorId] = [asStr(rec.id, `preset-${idx + 1}`), optStr(rec.floorId)];
  return {
    id, label: asStr(rec.label, id),
    position: parseVec3(rec.position, [0, 22, 26]), target: parseVec3(rec.target, [0, 2, 0]),
    up: parseVec3(rec.up, [0, 1, 0]), fov: asNum(rec.fov, 40),
    ...(floorId !== undefined ? { floorId } : {}),
  };
}

function parseHaBindingEntry(raw: unknown, idx: number, fallbackKey?: string): HaEntityBindingSpec | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const [id, entityId] = [asStr(rec.id, fallbackKey ?? `binding-${idx + 1}`), asStr(rec.entityId ?? rec.entity_id, fallbackKey ?? '')];
  if (!entityId) return null;
  const metricRaw = asStr(rec.metricType ?? rec.type, 'state') as HaMetricType;
  const metricType: HaMetricType = VALID_HA_METRIC_TYPES.includes(metricRaw) ? metricRaw : 'state';
  const [roomId, fixtureId, unit] = [optStr(rec.roomId), optStr(rec.fixtureId), optStr(rec.unit)];
  return {
    id, entityId, metricType, label: asStr(rec.label, id),
    ...(roomId !== undefined ? { roomId } : {}),
    ...(fixtureId !== undefined ? { fixtureId } : {}),
    ...(unit !== undefined ? { unit } : {}),
    interactive: typeof rec.interactive === 'boolean' ? rec.interactive : metricType === 'light' || metricType === 'cover',
  };
}

function parseHaBindings(raw: unknown): readonly HaEntityBindingSpec[] {
  if (Array.isArray(raw)) return raw.map((item, i) => parseHaBindingEntry(item, i)).filter((b): b is HaEntityBindingSpec => b !== null);
  const rec = asRecord(raw);
  return rec ? Object.entries(rec).map(([key, val], i) => parseHaBindingEntry(val, i, key)).filter((b): b is HaEntityBindingSpec => b !== null) : [];
}

export function parseFloorplanConfig(raw: unknown): FloorplanLayoutSpec {
  const outer = asRecord(raw);
  if (!outer) return DEFAULT_DEMO_FLOORPLAN_SPEC;
  const root = asRecord(outer.layout) ?? outer;
  const floorsRaw = Array.isArray(root.floors) ? root.floors : [];
  const floors = floorsRaw.map((f, i) => parseFloor(f, i)).filter((f): f is FloorSpec => f !== null);
  const firstFloor = floors[0];
  if (!firstFloor) return DEFAULT_DEMO_FLOORPLAN_SPEC;
  const presetsRaw = Array.isArray(root.cameraPresets) ? root.cameraPresets : [];
  const parsedPresets = presetsRaw.map((p, i) => parseCameraPreset(p, i)).filter((p): p is CameraPresetSpec => p !== null);
  const cameraPresets: readonly CameraPresetSpec[] =
    parsedPresets.length > 0
      ? parsedPresets
      : [
          { id: 'dollhouse', label: 'Overview', position: [0, 22, 26], target: [0, 2, 0], up: [0, 1, 0], fov: 40, floorId: firstFloor.id },
          { id: 'topdown', label: 'Top-Down', position: [0, 38, 0.01], target: [0, 0, 0], up: [0, 0, -1], fov: 38, floorId: firstFloor.id },
        ];
  const firstPreset = cameraPresets[0];
  return {
    title: asStr(root.title, '3D Floorplan'),
    defaultFloorId: asStr(root.defaultFloorId, firstFloor.id),
    defaultPresetId: asStr(root.defaultPresetId, firstPreset ? firstPreset.id : 'dollhouse'),
    floors, cameraPresets, haBindings: parseHaBindings(root.haBindings),
  };
}

export function fixtureToBox3(fixture: FloorplanFixtureSpec): Box3 {
  return new Box3(
    new Vector3(fixture.bounds.xMin, fixture.yMin, fixture.bounds.zMin),
    new Vector3(fixture.bounds.xMax, fixture.yMax, fixture.bounds.zMax),
  );
}

export function fixtureCenter(fixture: FloorplanFixtureSpec): Vector3 {
  return fixtureToBox3(fixture).getCenter(new Vector3());
}

export function fixtureSize(fixture: FloorplanFixtureSpec): Vector3 {
  return fixtureToBox3(fixture).getSize(new Vector3());
}

export function roomToBox3(room: FloorplanRoomSpec): Box3 {
  return new Box3(
    new Vector3(room.bounds.xMin, room.yMin, room.bounds.zMin),
    new Vector3(room.bounds.xMax, room.yMax, room.bounds.zMax),
  );
}

export function isBoundsContainedIn(inner: Bounds2D, outer: Bounds2D, epsilon = 1e-6): boolean {
  return (
    inner.xMin >= outer.xMin - epsilon &&
    inner.xMax <= outer.xMax + epsilon &&
    inner.zMin >= outer.zMin - epsilon &&
    inner.zMax <= outer.zMax + epsilon
  );
}

export function doBoundsOverlap2D(a: Bounds2D, b: Bounds2D, epsilon = 1e-6): boolean {
  return a.xMin + epsilon < b.xMax && a.xMax - epsilon > b.xMin && a.zMin + epsilon < b.zMax && a.zMax - epsilon > b.zMin;
}

export function findFloorCollisions(
  fixtures: readonly FloorplanFixtureSpec[],
  epsilon = 1e-6,
): [FloorplanFixtureSpec, FloorplanFixtureSpec][] {
  const solid = fixtures.filter((f) => f.solidFootprint);
  const collisions: [FloorplanFixtureSpec, FloorplanFixtureSpec][] = [];
  for (let i = 0; i < solid.length; i += 1) {
    const a = solid[i];
    if (!a) continue;
    for (let j = i + 1; j < solid.length; j += 1) {
      const b = solid[j];
      if (b?.roomId === a.roomId && doBoundsOverlap2D(a.bounds, b.bounds, epsilon)) {
        collisions.push([a, b]);
      }
    }
  }
  return collisions;
}
