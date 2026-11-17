import { Box3, type BoxGeometry, Color, Mesh, Vector3, type MeshStandardMaterial, type Object3D } from 'three';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  buildFloorplanScene,
  isUnmockedJsdomCanvas,
  type BuildFloorplanSceneOptions,
  type FloorplanEntityMap,
  type FloorplanEntityStateLike,
  type FloorplanFloorSpec,
  type FloorplanSceneBuildResult,
  type FloorplanSceneLights,
} from './buildFloorplanScene';
import { buildFloorFixtures, type FloorFixturesBuildResult } from './floorplanFixtures';
import {
  DEFAULT_DEMO_FLOORPLAN_SPEC,
  VALID_FACING_DIRECTIONS,
  VALID_FIXTURE_KINDS,
  VALID_FLOOR_MATERIALS,
  fixtureToBox3,
  parseFloorplanConfig,
  type FloorplanFixtureSpec,
} from './floorplanSpec';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('floorplanFixtures & buildFloorplanScene — Generic Config-Driven 3D Scene Builder', () => {
  it('builds every FixtureKind and FacingDirection strictly within its configured 3D bounding box', () => {
    const fixtures: FloorplanFixtureSpec[] = VALID_FIXTURE_KINDS.map((kind, i) => ({
      id: `fix-${kind}`,
      roomId: 'room-1',
      label: `Fixture ${kind}`,
      kind,
      facing: VALID_FACING_DIRECTIONS[i % VALID_FACING_DIRECTIONS.length]!,
      bounds: { xMin: i * 4, xMax: i * 4 + 2.4, zMin: -2, zMax: 1.2 },
      yMin: 0,
      yMax: kind === 'rug' ? 0.04 : 2.8,
      primaryColor: '#6b4c35',
      ...(i % 2 === 0 ? { accentColor: '#cbd5e1' } : {}),
      solidFootprint: kind !== 'rug',
    }));

    const floor: FloorplanFloorSpec = {
      id: 'test-floor',
      label: 'Test Floor',
      level: 1,
      elevationY: 0,
      ceilingHeight: 9,
      wallThickness: 0.4,
      bounds: { xMin: -2, xMax: 100, zMin: -4, zMax: 4 },
      rooms: [
        {
          id: 'room-1',
          label: 'Main Room',
          bounds: { xMin: -2, xMax: 100, zMin: -4, zMax: 4 },
          yMin: 0,
          yMax: 9,
          floorMaterial: 'wood',
          floorColor: '#9e6b3b',
          wallColor: '#f3efe6',
        },
      ],
      walls: [
        {
          id: 'low-wall',
          x1: -2,
          z1: -4,
          x2: 8,
          z2: -4,
          heightStart: 0.6,
          heightEnd: 0.6,
          thickness: 0.4,
          color: '#f3efe6',
          openings: [],
        },
      ],
      fixtures,
    };

    const built: FloorFixturesBuildResult = buildFloorFixtures(floor);
    built.root.updateMatrixWorld(true);

    expect(Object.keys(built.fixtures)).toHaveLength(VALID_FIXTURE_KINDS.length);

    for (const spec of fixtures) {
      const group = built.fixtures[spec.id]!;
      expect(group).toBeDefined();
      expect(group.userData.fixtureId).toBe(spec.id);
      expect(group.userData.roomId).toBe(spec.roomId);

      const meshes = group.children.filter((c: Object3D): c is Mesh => c instanceof Mesh);
      expect(meshes.length).toBeGreaterThanOrEqual(2);

      const worldBox = new Box3().setFromObject(group);
      const specBox = fixtureToBox3(spec);
      expect(worldBox.min.x).toBeGreaterThanOrEqual(specBox.min.x - 1e-4);
      expect(worldBox.max.x).toBeLessThanOrEqual(specBox.max.x + 1e-4);
      expect(worldBox.min.y).toBeGreaterThanOrEqual(specBox.min.y - 1e-4);
      expect(worldBox.max.y).toBeLessThanOrEqual(specBox.max.y + 1e-4);
      expect(worldBox.min.z).toBeGreaterThanOrEqual(specBox.min.z - 1e-4);
      expect(worldBox.max.z).toBeLessThanOrEqual(specBox.max.z + 1e-4);
    }

    const overrideScene = buildFloorplanScene({ floor });
    expect(overrideScene.floor.id).toBe('test-floor');
    overrideScene.dispose();

    const sampleMesh = built.fixtures['fix-desk']!.children[0] as Mesh;
    const geomDispose = vi.spyOn(sampleMesh.geometry, 'dispose');
    const matDispose = vi.spyOn(sampleMesh.material as MeshStandardMaterial, 'dispose');
    built.dispose();
    expect(geomDispose).toHaveBeenCalled();
    expect(matDispose).toHaveBeenCalled();
  });

  it('builds default demo studio scene with cutaway walls, openings, lights, upper massing, and HA entity updates', () => {
    const initialEntities: FloorplanEntityMap = {
      'light.studio_main': { state: 'on', attributes: { brightness: 200 } },
      'sensor.studio_temperature': { state: '71.5', attributes: { unit_of_measurement: '°F' } },
      'light.entryway_main': 'off',
      'entry-door': { state: 'open' },
    };
    const opts: BuildFloorplanSceneOptions = {
      showUpperFloors: true,
      background: '#141922',
      initialEntities,
    };

    const built: FloorplanSceneBuildResult = buildFloorplanScene(opts);
    const lights: FloorplanSceneLights = built.lights;

    expect(built.scene.background).toEqual(new Color('#141922'));
    expect(built.floor.id).toBe(DEFAULT_DEMO_FLOORPLAN_SPEC.defaultFloorId);
    expect(Object.keys(built.rooms)).toEqual(['studio', 'entryway']);
    expect(Object.keys(built.wallsById).length).toBeGreaterThanOrEqual(4);
    expect(built.upperFloorsGroup.visible).toBe(true);

    expect(lights.roomPoints.studio?.intensity).toBeGreaterThan(0);
    expect(lights.roomPoints.entryway?.intensity).toBe(0);
    expect(built.roomFloors.studio?.userData.lightState).toBe('on');
    expect(built.roomFloors.studio?.userData.temperature).toBe('71.5°F');
    expect(built.fixtures['entry-cabinet']?.userData.status).toBe('open');
    expect(built.fixtures['entry-cabinet']?.userData.active).toBe(true);

    const idleState: FloorplanEntityStateLike = { state: 'closed' };
    built.updateEntities({
      'light.studio_main': { state: 'off' },
      'binary_sensor.entry_door': idleState,
    });
    expect(lights.roomPoints.studio?.intensity).toBe(0);
    expect(built.fixtures['entry-cabinet']?.userData.status).toBe('closed');
    expect(built.fixtures['entry-cabinet']?.userData.active).toBe(false);

    built.setUpperFloorsVisible(false);
    expect(built.upperFloorsGroup.visible).toBe(false);

    const slabGeomSpy = vi.spyOn(built.roomFloors.studio!.geometry, 'dispose');
    built.dispose();
    expect(slabGeomSpy).toHaveBeenCalled();
  });

  it('renders procedural CanvasTexture, handles canvas context errors, non-jsdom UA, and multi-floor layouts', () => {
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValueOnce('Mozilla/5.0 Chrome/130.0');
    expect(isUnmockedJsdomCanvas()).toBe(false);

    const fakeCtx = {
      fillStyle: '',
      strokeStyle: '',
      lineWidth: 1,
      fillRect: vi.fn(),
      beginPath: vi.fn(),
      moveTo: vi.fn(),
      lineTo: vi.fn(),
      stroke: vi.fn(),
    } as unknown as CanvasRenderingContext2D;

    let callIdx = 0;
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(((kind: string) => {
      if (kind !== '2d') return null;
      callIdx += 1;
      if (callIdx === 5) throw new Error('2d context failure');
      return fakeCtx;
    }) as never);

    const multiFloorLayout = parseFloorplanConfig({
      title: 'Multi-Floor Config',
      defaultFloorId: 'floor-1',
      floors: [
        {
          id: 'floor-1',
          label: 'Level 1',
          elevationY: 0,
          ceilingHeight: 9,
          rooms: VALID_FLOOR_MATERIALS.map((mat, i) => ({
            id: `room-${mat}`,
            label: `Room ${mat}`,
            bounds: { xMin: i * 5, xMax: (i + 1) * 5, zMin: -4, zMax: 4 },
            floorMaterial: mat,
            floorColor: '#887766',
          })),
          walls: [
            {
              id: 'vert-wall',
              x1: 0,
              z1: -4,
              x2: 0,
              z2: 4,
              heightStart: 9,
              heightEnd: 5,
              openings: [{ id: 'arch-1', type: 'archway', offsetStart: 2, offsetEnd: 5, yMin: 0, yMax: 4 }],
            },
          ],
          fixtures: [
            {
              id: 'power-app',
              roomId: 'room-wood',
              kind: 'appliance',
              bounds: { xMin: 1, xMax: 3, zMin: -2, zMax: 0 },
            },
          ],
        },
        {
          id: 'floor-2',
          label: 'Level 2',
          elevationY: 10,
          ceilingHeight: 8.5,
          rooms: [{ id: 'upper-room', bounds: { xMin: 0, xMax: 10, zMin: -4, zMax: 4 } }],
        },
      ],
      haBindings: [
        {
          id: 'app-power',
          entityId: 'sensor.appliance_watts',
          metricType: 'power',
          roomId: 'room-wood',
          fixtureId: 'power-app',
          unit: 'W',
        },
        {
          id: 'room-hum',
          entityId: 'sensor.room_humidity',
          metricType: 'humidity',
          roomId: 'room-wood',
        },
      ],
    });

    const built = buildFloorplanScene({
      layout: multiFloorLayout,
      floorId: 'floor-1',
      background: null,
      initialEntities: {
        'sensor.appliance_watts': '45.2',
        'sensor.room_humidity': '48',
      },
    });

    expect(built.scene.background).toBeNull();
    expect(fakeCtx.fillRect).toHaveBeenCalled();
    expect(built.upperFloorsGroup.children.length).toBe(1);
    expect(built.fixtures['power-app']?.userData.power).toBe('45.2W');
    expect(built.fixtures['power-app']?.userData.active).toBe(true);
    expect(built.roomFloors['room-wood']?.userData.humidity).toBe('48');

    built.updateEntities({ 'sensor.appliance_watts': 'on' });
    expect(built.fixtures['power-app']?.userData.active).toBe(true);
    built.updateEntities({ 'sensor.appliance_watts': '1.0' });
    expect(built.fixtures['power-app']?.userData.active).toBe(false);
    built.dispose();
  });

  it('builds diagonal walls along true Euclidean span from (0,0) to (4,4) instead of axis-snapping', () => {
    const layout = parseFloorplanConfig({
      floors: [
        {
          id: 'diag-floor',
          bounds: { xMin: -2, xMax: 6, zMin: -2, zMax: 6 },
          rooms: [{ id: 'room-1', bounds: { xMin: -2, xMax: 6, zMin: -2, zMax: 6 } }],
          walls: [
            {
              id: 'diag-wall',
              x1: 0,
              z1: 0,
              x2: 4,
              z2: 4,
              heightStart: 8,
              heightEnd: 8,
              thickness: 0.4,
            },
          ],
        },
      ],
    });

    const built = buildFloorplanScene({ layout });
    const wg = built.wallsById['diag-wall']!;
    expect(wg).toBeDefined();

    const bodyMesh = wg.children.find((c): c is Mesh => c instanceof Mesh && c.name === 'diag-wall-body-0')!;
    expect(bodyMesh).toBeDefined();

    const geomParams = (bodyMesh.geometry as BoxGeometry).parameters;
    const containsWorldPoint = (wx: number, wy: number, wz: number): boolean => {
      const local = bodyMesh.worldToLocal(new Vector3(wx, wy, wz));
      return (
        Math.abs(local.x) <= geomParams.width / 2 + 1e-4 &&
        Math.abs(local.y) <= geomParams.height / 2 + 1e-4 &&
        Math.abs(local.z) <= geomParams.depth / 2 + 1e-4
      );
    };

    expect(containsWorldPoint(0, 1, 0)).toBe(true);
    expect(containsWorldPoint(2, 1, 2)).toBe(true);
    expect(containsWorldPoint(4, 1, 4)).toBe(true);
    expect(containsWorldPoint(0, 1, 2)).toBe(false);
    expect(containsWorldPoint(4, 1, 2)).toBe(false);

    const worldBox = new Box3().setFromObject(wg);
    expect(worldBox.min.x).toBeLessThan(0.05);
    expect(worldBox.min.z).toBeLessThan(0.05);
    expect(worldBox.max.x).toBeGreaterThan(3.95);
    expect(worldBox.max.z).toBeGreaterThan(3.95);

    built.dispose();
  });

  it('positions upper floor massing relative to selected floor elevationY and omits massing on the highest floor', () => {
    const layout = parseFloorplanConfig({
      floors: [
        {
          id: 'floor-1',
          level: 1,
          elevationY: 0,
          ceilingHeight: 9,
          rooms: [{ id: 'r1', bounds: { xMin: -5, xMax: 5, zMin: -5, zMax: 5 } }],
        },
        {
          id: 'floor-2',
          level: 2,
          elevationY: 10,
          ceilingHeight: 8,
          rooms: [{ id: 'r2', bounds: { xMin: -4, xMax: 4, zMin: -4, zMax: 4 } }],
        },
        {
          id: 'floor-3',
          level: 3,
          elevationY: 19,
          ceilingHeight: 8,
          rooms: [{ id: 'r3', bounds: { xMin: -3, xMax: 3, zMin: -3, zMax: 3 } }],
        },
      ],
    });

    const fromFirst = buildFloorplanScene({ layout, floorId: 'floor-1', showUpperFloors: true });
    expect(fromFirst.upperFloorsGroup.children.map((c) => c.name)).toEqual(['massing-floor-2', 'massing-floor-3']);
    const f2FromFirst = new Box3().setFromObject(fromFirst.upperFloorsGroup.getObjectByName('massing-floor-2')!);
    expect(f2FromFirst.min.y).toBeCloseTo(10, 4);
    expect(f2FromFirst.max.y).toBeCloseTo(10 + 8 * 0.45, 4);
    fromFirst.dispose();

    const fromSecond = buildFloorplanScene({ layout, floorId: 'floor-2', showUpperFloors: true });
    expect(fromSecond.upperFloorsGroup.children.map((c) => c.name)).toEqual(['massing-floor-3']);
    const f3FromSecond = new Box3().setFromObject(fromSecond.upperFloorsGroup.getObjectByName('massing-floor-3')!);
    expect(f3FromSecond.min.y).toBeCloseTo(9, 4);
    expect(f3FromSecond.max.y).toBeCloseTo(9 + 8 * 0.45, 4);
    fromSecond.dispose();

    const fromHighest = buildFloorplanScene({ layout, floorId: 'floor-3', showUpperFloors: true });
    expect(fromHighest.upperFloorsGroup.children).toHaveLength(0);
    fromHighest.dispose();

    const sameElevationLayout = parseFloorplanConfig({
      floors: [
        {
          id: 'lvl-1',
          level: 1,
          elevationY: 0,
          ceilingHeight: 9,
          rooms: [{ id: 'r1', bounds: { xMin: -4, xMax: 4, zMin: -4, zMax: 4 } }],
        },
        {
          id: 'lvl-2',
          level: 2,
          elevationY: 0,
          ceilingHeight: 8,
          rooms: [{ id: 'r2', bounds: { xMin: -4, xMax: 4, zMin: -4, zMax: 4 } }],
        },
      ],
    });
    const sameElevFrom1 = buildFloorplanScene({ layout: sameElevationLayout, floorId: 'lvl-1', showUpperFloors: true });
    expect(sameElevFrom1.upperFloorsGroup.children.map((c) => c.name)).toEqual(['massing-lvl-2']);
    const sameElevBox = new Box3().setFromObject(sameElevFrom1.upperFloorsGroup.getObjectByName('massing-lvl-2')!);
    expect(sameElevBox.min.y).toBeCloseTo(9.5, 4);
    sameElevFrom1.dispose();
  });
});
