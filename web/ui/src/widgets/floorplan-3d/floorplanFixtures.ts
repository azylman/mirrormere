import {
  BoxGeometry,
  Group,
  Mesh,
  MeshStandardMaterial,
  type BufferGeometry,
} from 'three';
import type {
  FacingDirection,
  FloorSpec,
  FloorplanFixtureSpec,
} from './floorplanSpec';

export interface FloorFixturesBuildResult {
  root: Group;
  fixtures: Readonly<Record<string, Group>>;
  statusMaterials: Readonly<Record<string, MeshStandardMaterial>>;
  dispose: () => void;
}

type NormalizedPart = readonly [
  ux0: number,
  ux1: number,
  uy0: number,
  uy1: number,
  uz0: number,
  uz1: number,
  mat: MeshStandardMaterial,
];

interface FixtureMaterialSet {
  primary: MeshStandardMaterial;
  accent: MeshStandardMaterial;
  status: MeshStandardMaterial;
  darkFrame: MeshStandardMaterial;
  lightSurface: MeshStandardMaterial;
  glass: MeshStandardMaterial;
  foliage: MeshStandardMaterial;
}

function rotateUnitHorizontal(
  ux0: number,
  ux1: number,
  uz0: number,
  uz1: number,
  facing: FacingDirection,
): readonly [number, number, number, number] {
  if (facing === '-Z') {
    return [1 - ux1, 1 - ux0, 1 - uz1, 1 - uz0];
  }
  if (facing === '+X') {
    return [uz0, uz1, 1 - ux1, 1 - ux0];
  }
  if (facing === '-X') {
    return [1 - uz1, 1 - uz0, ux0, ux1];
  }
  return [ux0, ux1, uz0, uz1];
}

function buildSeatingAndSurfaceParts(
  spec: FloorplanFixtureSpec,
  mats: FixtureMaterialSet,
): readonly NormalizedPart[] {
  const { primary, accent, status, darkFrame, lightSurface } = mats;
  switch (spec.kind) {
    case 'desk':
      return [
        [0.02, 0.10, 0.0, 0.72, 0.06, 0.94, darkFrame],
        [0.90, 0.98, 0.0, 0.72, 0.06, 0.94, darkFrame],
        [0.0, 1.0, 0.72, 0.80, 0.0, 1.0, primary],
        [0.34, 0.66, 0.80, 0.98, 0.10, 0.24, status],
      ];
    case 'chair':
      return [
        [0.18, 0.82, 0.0, 0.42, 0.18, 0.82, darkFrame],
        [0.08, 0.92, 0.42, 0.56, 0.10, 0.94, primary],
        [0.10, 0.90, 0.56, 1.0, 0.04, 0.24, primary],
        [0.04, 0.16, 0.56, 0.76, 0.20, 0.84, accent],
        [0.84, 0.96, 0.56, 0.76, 0.20, 0.84, accent],
      ];
    case 'sofa':
      return [
        [0.02, 0.98, 0.0, 0.16, 0.04, 0.96, darkFrame],
        [0.0, 1.0, 0.16, 0.48, 0.04, 0.98, primary],
        [0.0, 1.0, 0.48, 1.0, 0.0, 0.26, primary],
        [0.0, 0.12, 0.48, 0.74, 0.26, 0.96, primary],
        [0.88, 1.0, 0.48, 0.74, 0.26, 0.96, primary],
        [0.16, 0.48, 0.48, 0.76, 0.24, 0.46, accent],
        [0.52, 0.84, 0.48, 0.76, 0.24, 0.46, accent],
      ];
    case 'bed':
      return [
        [0.02, 0.98, 0.0, 0.30, 0.02, 0.98, primary],
        [0.06, 0.94, 0.30, 0.58, 0.10, 0.94, lightSurface],
        [0.0, 1.0, 0.20, 1.0, 0.0, 0.12, accent],
        [0.12, 0.46, 0.58, 0.72, 0.14, 0.34, accent],
        [0.54, 0.88, 0.58, 0.72, 0.14, 0.34, accent],
      ];
    case 'table':
      return [
        [0.0, 1.0, 0.86, 1.0, 0.0, 1.0, primary],
        [0.04, 0.14, 0.0, 0.86, 0.04, 0.14, accent],
        [0.86, 0.96, 0.0, 0.86, 0.04, 0.14, accent],
        [0.04, 0.14, 0.0, 0.86, 0.86, 0.96, accent],
        [0.86, 0.96, 0.0, 0.86, 0.86, 0.96, accent],
      ];
    default:
      return [];
  }
}

function buildStorageAndBathParts(
  spec: FloorplanFixtureSpec,
  mats: FixtureMaterialSet,
): readonly NormalizedPart[] {
  const { primary, accent, status, darkFrame, lightSurface, glass } = mats;
  switch (spec.kind) {
    case 'cabinet':
      return [
        [0.0, 1.0, 0.0, 0.90, 0.0, 0.92, primary],
        [0.0, 1.0, 0.90, 1.0, 0.0, 1.0, accent],
        [0.04, 0.48, 0.08, 0.86, 0.92, 0.98, status],
        [0.52, 0.96, 0.08, 0.86, 0.92, 0.98, status],
      ];
    case 'bookcase':
      return [
        [0.0, 1.0, 0.0, 1.0, 0.0, 0.14, primary],
        [0.0, 0.08, 0.0, 1.0, 0.14, 1.0, primary],
        [0.92, 1.0, 0.0, 1.0, 0.14, 1.0, primary],
        [0.08, 0.92, 0.0, 0.08, 0.14, 1.0, primary],
        [0.08, 0.92, 0.46, 0.54, 0.14, 0.96, primary],
        [0.08, 0.92, 0.92, 1.0, 0.14, 1.0, primary],
        [0.14, 0.44, 0.08, 0.42, 0.24, 0.88, accent],
        [0.52, 0.84, 0.54, 0.88, 0.24, 0.88, status],
      ];
    case 'sink':
      return [
        [0.02, 0.98, 0.0, 0.78, 0.04, 0.94, primary],
        [0.0, 1.0, 0.78, 0.88, 0.0, 1.0, lightSurface],
        [0.20, 0.80, 0.84, 0.92, 0.20, 0.78, accent],
        [0.44, 0.56, 0.88, 1.0, 0.08, 0.28, darkFrame],
      ];
    case 'shower':
      return [
        [0.0, 1.0, 0.0, 0.08, 0.0, 1.0, primary],
        [0.92, 1.0, 0.08, 0.96, 0.0, 1.0, glass],
        [0.0, 1.0, 0.08, 0.96, 0.92, 1.0, glass],
        [0.35, 0.65, 0.90, 1.0, 0.06, 0.30, darkFrame],
      ];
    case 'toilet':
      return [
        [0.16, 0.84, 0.0, 0.52, 0.34, 0.96, lightSurface],
        [0.10, 0.90, 0.48, 1.0, 0.02, 0.38, lightSurface],
        [0.18, 0.82, 0.52, 0.60, 0.36, 0.94, accent],
      ];
    default:
      return [];
  }
}

function buildUtilityAndDecorParts(
  spec: FloorplanFixtureSpec,
  mats: FixtureMaterialSet,
): readonly NormalizedPart[] {
  const { primary, accent, status, darkFrame, lightSurface, foliage } = mats;
  switch (spec.kind) {
    case 'rug':
      return [
        [0.0, 1.0, 0.0, 0.55, 0.0, 1.0, primary],
        [0.08, 0.92, 0.55, 1.0, 0.08, 0.92, accent],
      ];
    case 'appliance':
      return [
        [0.0, 1.0, 0.0, 0.92, 0.0, 0.92, primary],
        [0.08, 0.92, 0.14, 0.80, 0.92, 0.98, darkFrame],
        [0.12, 0.88, 0.84, 0.98, 0.88, 1.0, status],
      ];
    case 'tv':
      return [
        [0.32, 0.68, 0.0, 0.16, 0.20, 0.80, darkFrame],
        [0.0, 1.0, 0.16, 1.0, 0.15, 0.55, darkFrame],
        [0.04, 0.96, 0.22, 0.94, 0.55, 0.85, status],
      ];
    case 'stairs':
      return [
        [0.0, 1.0, 0.0, 0.25, 0.0, 0.25, primary],
        [0.0, 1.0, 0.20, 0.50, 0.25, 0.50, primary],
        [0.0, 1.0, 0.45, 0.75, 0.50, 0.75, primary],
        [0.0, 1.0, 0.70, 1.0, 0.75, 1.0, primary],
        [0.90, 1.0, 0.0, 1.0, 0.0, 1.0, accent],
      ];
    case 'plant':
      return [
        [0.22, 0.78, 0.0, 0.36, 0.22, 0.78, primary],
        [0.42, 0.58, 0.36, 0.64, 0.42, 0.58, darkFrame],
        [0.08, 0.92, 0.54, 1.0, 0.08, 0.92, foliage],
      ];
    case 'lamp':
      return [
        [0.20, 0.80, 0.0, 0.08, 0.20, 0.80, darkFrame],
        [0.44, 0.56, 0.08, 0.72, 0.44, 0.56, darkFrame],
        [0.10, 0.90, 0.70, 1.0, 0.10, 0.90, status],
      ];
    case 'art':
      return [
        [0.0, 1.0, 0.0, 1.0, 0.0, 0.52, primary],
        [0.08, 0.92, 0.10, 0.90, 0.52, 1.0, lightSurface],
      ];
    case 'door':
    case 'custom':
      return [
        [0.0, 1.0, 0.0, 0.86, 0.0, 1.0, primary],
        [0.06, 0.94, 0.86, 1.0, 0.06, 0.94, status],
      ];
    default:
      return [];
  }
}

function getNormalizedParts(
  spec: FloorplanFixtureSpec,
  mats: FixtureMaterialSet,
): readonly NormalizedPart[] {
  const seating = buildSeatingAndSurfaceParts(spec, mats);
  if (seating.length > 0) return seating;
  const storage = buildStorageAndBathParts(spec, mats);
  if (storage.length > 0) return storage;
  return buildUtilityAndDecorParts(spec, mats);
}

export function buildFloorFixtures(floor: FloorSpec): FloorFixturesBuildResult {
  const geometries: BufferGeometry[] = [];
  const materials: MeshStandardMaterial[] = [];
  const matCache = new Map<string, MeshStandardMaterial>();

  const getMat = (
    color: string,
    roughness = 0.72,
    metalness = 0.06,
    opacity = 1,
  ): MeshStandardMaterial => {
    const key = `${color}|${roughness}|${metalness}|${opacity}`;
    const cached = matCache.get(key);
    if (cached) return cached;
    const created = new MeshStandardMaterial({
      color,
      roughness,
      metalness,
      transparent: opacity < 1,
      opacity,
    });
    materials.push(created);
    matCache.set(key, created);
    return created;
  };

  const darkFrame = getMat('#242830', 0.55, 0.45);
  const lightSurface = getMat('#f4f3ef', 0.42, 0.05);
  const glass = getMat('#9ac7e3', 0.15, 0.1, 0.36);
  const foliage = getMat('#3b6e4c', 0.78, 0.02);

  const root = new Group();
  root.name = `floor-${floor.id}-fixtures`;

  const fixtures: Record<string, Group> = {};
  const statusMaterials: Record<string, MeshStandardMaterial> = {};

  for (const spec of floor.fixtures) {
    const group = new Group();
    group.name = `fixture-${spec.id}`;
    group.userData = {
      fixtureId: spec.id,
      roomId: spec.roomId,
      room: spec.roomId,
      label: spec.label,
      kind: spec.kind,
      facing: spec.facing,
    };

    const primary = getMat(spec.primaryColor, spec.kind === 'rug' ? 0.9 : 0.65);
    const accentHex = spec.accentColor ?? spec.primaryColor;
    const accent = getMat(accentHex, 0.58);
    const status = new MeshStandardMaterial({
      color: accentHex,
      emissive: spec.kind === 'lamp' ? '#ffd999' : '#000000',
      emissiveIntensity: spec.kind === 'lamp' ? 0.35 : 0.05,
      roughness: 0.45,
      metalness: 0.15,
    });
    materials.push(status);
    statusMaterials[spec.id] = status;

    const parts = getNormalizedParts(spec, {
      primary,
      accent,
      status,
      darkFrame,
      lightSurface,
      glass,
      foliage,
    });

    const spanX = Math.max(0.02, spec.bounds.xMax - spec.bounds.xMin);
    const spanY = Math.max(0.01, spec.yMax - spec.yMin);
    const spanZ = Math.max(0.02, spec.bounds.zMax - spec.bounds.zMin);

    for (const [idx, [ux0, ux1, uy0, uy1, uz0, uz1, partMat]] of parts.entries()) {
      const [rx0, rx1, rz0, rz1] = rotateUnitHorizontal(ux0, ux1, uz0, uz1, spec.facing);
      const x0 = spec.bounds.xMin + spanX * rx0;
      const x1 = spec.bounds.xMin + spanX * rx1;
      const y0 = spec.yMin + spanY * uy0;
      const y1 = spec.yMin + spanY * uy1;
      const z0 = spec.bounds.zMin + spanZ * rz0;
      const z1 = spec.bounds.zMin + spanZ * rz1;

      const geom = new BoxGeometry(
        Math.max(0.005, x1 - x0),
        Math.max(0.005, y1 - y0),
        Math.max(0.005, z1 - z0),
      );
      geometries.push(geom);
      const mesh = new Mesh(geom, partMat);
      mesh.name = `${spec.id}-part-${idx}`;
      mesh.position.set((x0 + x1) / 2, (y0 + y1) / 2, (z0 + z1) / 2);
      mesh.castShadow = spec.kind !== 'rug';
      mesh.receiveShadow = true;
      mesh.userData = {
        fixtureId: spec.id,
        roomId: spec.roomId,
        room: spec.roomId,
        label: spec.label,
        kind: spec.kind,
      };
      group.add(mesh);
    }

    fixtures[spec.id] = group;
    root.add(group);
  }

  return {
    root,
    fixtures,
    statusMaterials,
    dispose: () => {
      for (const geom of geometries) geom.dispose();
      for (const mat of materials) mat.dispose();
    },
  };
}
