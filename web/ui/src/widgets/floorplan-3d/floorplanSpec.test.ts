import { describe, expect, it } from 'vitest';
import {
  DEFAULT_DEMO_FLOORPLAN_SPEC,
  doBoundsOverlap2D,
  findFloorCollisions,
  fixtureCenter,
  fixtureSize,
  fixtureToBox3,
  isBoundsContainedIn,
  parseFloorplanConfig,
  roomToBox3,
  type Bounds2D,
  type CameraPresetSpec,
  type FloorSpec,
  type FloorplanFixtureSpec,
  type FloorplanLayoutSpec,
  type FloorplanRoomSpec,
  type FloorplanWallSpec,
  type HaEntityBindingSpec,
  type Vec3Tuple,
  type WallOpeningSpec,
  type WallOpeningType,
} from './floorplanSpec';

describe('floorplanSpec — Generic Config-Driven 3D Floorplan Schema & Validation', () => {
  it('defines a generic 2-room demo studio fallback with valid bounds, openings, fixtures, presets, and HA bindings', () => {
    const spec: FloorplanLayoutSpec = DEFAULT_DEMO_FLOORPLAN_SPEC;
    expect(spec.title).toBe('Demo Studio Floorplan');
    expect(spec.defaultFloorId).toBe('floor-1');
    expect(spec.defaultPresetId).toBe('dollhouse');
    expect(spec.floors).toHaveLength(1);

    const floor: FloorSpec = spec.floors[0]!;
    expect(floor.id).toBe('floor-1');
    expect(floor.rooms).toHaveLength(2);
    expect(floor.rooms.map((r: FloorplanRoomSpec) => r.id)).toEqual(['studio', 'entryway']);
    expect(floor.walls.length).toBeGreaterThanOrEqual(4);

    for (const room of floor.rooms) {
      expect(isBoundsContainedIn(room.bounds, floor.bounds)).toBe(true);
      const roomBox = roomToBox3(room);
      expect(roomBox.max.y - roomBox.min.y).toBeCloseTo(floor.ceilingHeight, 5);
    }

    for (const fixture of floor.fixtures) {
      const parentRoom = floor.rooms.find((r) => r.id === fixture.roomId)!;
      expect(parentRoom).toBeDefined();
      expect(isBoundsContainedIn(fixture.bounds, parentRoom.bounds)).toBe(true);
      const box = fixtureToBox3(fixture);
      const center = fixtureCenter(fixture);
      const size = fixtureSize(fixture);
      expect(size.x).toBeCloseTo(fixture.bounds.xMax - fixture.bounds.xMin, 5);
      expect(center.x).toBeCloseTo((box.min.x + box.max.x) / 2, 5);
    }

    expect(findFloorCollisions(floor.fixtures)).toEqual([]);
    expect(spec.cameraPresets.map((p: CameraPresetSpec) => p.id)).toContain('dollhouse');
    expect(spec.haBindings.length).toBeGreaterThanOrEqual(4);
  });

  it('returns DEFAULT_DEMO_FLOORPLAN_SPEC when config is null, non-object, or missing valid floors', () => {
    expect(parseFloorplanConfig(null)).toBe(DEFAULT_DEMO_FLOORPLAN_SPEC);
    expect(parseFloorplanConfig(undefined)).toBe(DEFAULT_DEMO_FLOORPLAN_SPEC);
    expect(parseFloorplanConfig('invalid')).toBe(DEFAULT_DEMO_FLOORPLAN_SPEC);
    expect(parseFloorplanConfig([1, 2, 3])).toBe(DEFAULT_DEMO_FLOORPLAN_SPEC);
    expect(parseFloorplanConfig({})).toBe(DEFAULT_DEMO_FLOORPLAN_SPEC);
    expect(parseFloorplanConfig({ floors: [] })).toBe(DEFAULT_DEMO_FLOORPLAN_SPEC);
    expect(parseFloorplanConfig({ floors: [null, { id: 'empty-floor', rooms: ['bad'] }] })).toBe(
      DEFAULT_DEMO_FLOORPLAN_SPEC,
    );
  });

  it('parses multi-floor layouts from config.layout or root config with rooms, walls, openings, and fixtures', () => {
    const openingType: WallOpeningType = 'archway';
    const upVec: Vec3Tuple = [0, 1, 0];
    const customConfig = {
      layout: {
        title: 'Two-Story Workspace',
        defaultFloorId: 'level-2',
        defaultPresetId: 'loft-cam',
        floors: [
          {
            id: 'level-1',
            label: 'Ground Level',
            level: 1,
            elevationY: 0,
            ceilingHeight: 9,
            wallThickness: 0.5,
            bounds: { xMin: -10, xMax: 10, zMin: -8, zMax: 8 },
            rooms: [
              null,
              {
                id: 'lobby',
                label: 'Main Lobby',
                xMin: 10,
                xMax: -10,
                zMin: 8,
                zMax: -8,
                floorMaterial: 'stone',
                floorColor: '#777777',
                wallColor: '#eeeeee',
              },
            ],
            walls: [
              null,
              {
                id: 'north-wall',
                x1: -10,
                z1: -8,
                x2: 10,
                z2: -8,
                height: 9,
                openings: [
                  null,
                  { id: 'win-1', type: 'window', offsetStart: 2, offsetEnd: 6 },
                  { id: 'arch-1', type: openingType, offsetStart: 7, offsetEnd: 9, yMin: 0, yMax: 7 },
                  { id: 'door-fallback', type: 'unknown', offsetStart: 10, offsetEnd: 13 },
                ],
              },
            ],
            fixtures: [
              null,
              {
                id: 'lobby-desk',
                room: 'lobby',
                label: 'Reception Desk',
                kind: 'desk',
                facing: '-X',
                position: [0, 0, -2],
                size: [4, 2.6, 2],
                primaryColor: '#553311',
                accentColor: '#222222',
              },
              {
                id: 'lobby-art',
                label: 'Wall Poster',
                category: 'invalid-kind',
                facing: 'invalid-dir',
                bounds: { xMin: -2, xMax: 2, zMin: -7.9, zMax: -7.7 },
                color: '#112233',
                solidFootprint: false,
              },
              {
                id: 'lobby-rug',
                kind: 'rug',
                facing: 'up',
                bounds: { xMin: -3, xMax: 3, zMin: -3, zMax: 3 },
              },
            ],
          },
          {
            rooms: [
              {
                id: 'loft',
                floorMaterial: 'invalid-material',
                bounds: { xMin: -8, xMax: 8, zMin: -6, zMax: 6 },
              },
            ],
          },
        ],
        cameraPresets: [
          null,
          {
            id: 'loft-cam',
            label: 'Loft View',
            position: [0, 20, 22],
            target: [0, 10, 0],
            up: upVec,
            fov: 45,
            floorId: 'floor-2',
          },
          {
            id: 'fallback-vec-cam',
            position: 'bad-vec',
          },
        ],
        haBindings: {
          'light.lobby_ceiling': {
            metricType: 'light',
            label: 'Lobby Ceiling',
            roomId: 'lobby',
          },
          customSensor: {
            entity_id: 'sensor.lobby_power',
            type: 'power',
            roomId: 'lobby',
            fixtureId: 'lobby-desk',
            unit: 'W',
            interactive: false,
          },
          unknownMetric: {
            entityId: 'sensor.misc',
            metricType: 'nonexistent',
          },
          invalidEntry: null,
        },
      },
    };

    const parsed = parseFloorplanConfig(customConfig);
    expect(parsed.title).toBe('Two-Story Workspace');
    expect(parsed.defaultFloorId).toBe('level-2');
    expect(parsed.defaultPresetId).toBe('loft-cam');
    expect(parsed.floors).toHaveLength(2);

    const f1 = parsed.floors[0]!;
    expect(f1.rooms[0]?.bounds).toEqual({ xMin: -10, xMax: 10, zMin: -8, zMax: 8 });
    expect(f1.rooms[0]?.floorMaterial).toBe('stone');

    const w1: FloorplanWallSpec = f1.walls[0]!;
    expect(w1.openings).toHaveLength(3);
    const winOpening: WallOpeningSpec = w1.openings[0]!;
    expect(winOpening.type).toBe('window');
    expect(winOpening.yMin).toBe(2.5);
    expect(w1.openings[1]?.type).toBe('archway');
    expect(w1.openings[2]?.type).toBe('door');

    const desk: FloorplanFixtureSpec = f1.fixtures[0]!;
    expect(desk.bounds).toEqual({ xMin: -2, xMax: 2, zMin: -3, zMax: -1 });
    expect(desk.yMax).toBeCloseTo(2.6, 5);
    expect(desk.accentColor).toBe('#222222');
    expect(desk.solidFootprint).toBe(true);

    const art: FloorplanFixtureSpec = f1.fixtures[1]!;
    expect(art.kind).toBe('custom');
    expect(art.facing).toBe('+Z');
    expect(art.solidFootprint).toBe(false);

    const rug: FloorplanFixtureSpec = f1.fixtures[2]!;
    expect(rug.yMax).toBeCloseTo(0.04, 5);
    expect(rug.solidFootprint).toBe(false);

    const f2 = parsed.floors[1]!;
    expect(f2.id).toBe('floor-2');
    expect(f2.rooms[0]?.floorMaterial).toBe('wood');
    expect(f2.bounds).toEqual({ xMin: -8, xMax: 8, zMin: -6, zMax: 6 });

    expect(parsed.cameraPresets).toHaveLength(2);
    expect(parsed.cameraPresets[0]?.floorId).toBe('floor-2');
    expect(parsed.cameraPresets[1]?.position).toEqual([0, 22, 26]);

    expect(parsed.haBindings).toHaveLength(3);
    const b0: HaEntityBindingSpec = parsed.haBindings[0]!;
    expect(b0.entityId).toBe('light.lobby_ceiling');
    expect(b0.interactive).toBe(true);
    const b1: HaEntityBindingSpec = parsed.haBindings[1]!;
    expect(b1.entityId).toBe('sensor.lobby_power');
    expect(b1.unit).toBe('W');
    expect(b1.fixtureId).toBe('lobby-desk');
    expect(parsed.haBindings[2]?.metricType).toBe('state');
  });

  it('generates default camera presets and parses array haBindings when presets are omitted', () => {
    const minimal = parseFloorplanConfig({
      floors: [
        {
          id: 'main',
          rooms: [{ id: 'room-a', bounds: { xMin: -5, xMax: 5, zMin: -5, zMax: 5 } }],
        },
      ],
      haBindings: [
        null,
        { id: 'empty-entity', entityId: '   ' },
        { id: 'b1', entityId: 'cover.shade', metricType: 'cover' },
      ],
    });
    expect(minimal.cameraPresets).toHaveLength(2);
    expect(minimal.defaultPresetId).toBe('dollhouse');
    expect(minimal.haBindings).toHaveLength(1);
    expect(minimal.haBindings[0]?.interactive).toBe(true);
  });

  it('detects 2D bounds overlaps and solid fixture collisions within the same room', () => {
    const a: Bounds2D = { xMin: 0, xMax: 4, zMin: 0, zMax: 4 };
    const b: Bounds2D = { xMin: 2, xMax: 6, zMin: 2, zMax: 6 };
    const c: Bounds2D = { xMin: 4, xMax: 8, zMin: 0, zMax: 4 };
    expect(doBoundsOverlap2D(a, b)).toBe(true);
    expect(doBoundsOverlap2D(a, c)).toBe(false);

    const f1: FloorplanFixtureSpec = {
      id: 'f1', roomId: 'r1', label: 'Desk 1', kind: 'desk', facing: '+Z',
      bounds: a, yMin: 0, yMax: 2.5, primaryColor: '#111111', solidFootprint: true,
    };
    const f2: FloorplanFixtureSpec = {
      id: 'f2', roomId: 'r1', label: 'Desk 2', kind: 'desk', facing: '+Z',
      bounds: b, yMin: 0, yMax: 2.5, primaryColor: '#222222', solidFootprint: true,
    };
    const f3OtherRoom: FloorplanFixtureSpec = {
      ...f2, id: 'f3', roomId: 'r2',
    };
    expect(findFloorCollisions([f1, f2, f3OtherRoom])).toEqual([[f1, f2]]);
  });
});
