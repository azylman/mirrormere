import { describe, expect, it } from 'vitest';
import type { LayoutEditorWidget } from '../sse/types';
import {
  canPlaceWidget,
  compactScreens,
  computeScreenMask,
  computeWidgetBitmask,
  getOccupiedCellCount,
  getTotalScreens,
  moveOrSwapWidget,
  moveWidgetToScreen,
  resizeWidget,
  resolvePreviewOrigin,
  togglePinWidget,
} from './editorMath';
import { itemAt } from '../test-utils/lookup';

const makeWidget = (overrides: Partial<LayoutEditorWidget> = {}): LayoutEditorWidget => ({
  widget_id: 'w1',
  type: 'clock',
  screen: 0,
  origin: [0, 0],
  dimensions: [2, 1],
  pinned: false,
  supported_dimensions: [[2, 1], [3, 2]],
  ...overrides,
});

describe('editorMath', () => {
  describe('computeWidgetBitmask', () => {
    // Bit index = row * cols + col.
    const tests: { name: string; origin: [number, number]; dims: [number, number]; cols: number; want: number }[] = [
      { name: '1x1 at origin [0, 0]', origin: [0, 0], dims: [1, 1], cols: 6, want: 1 },
      { name: '2x1 at origin [0, 0] on 6-col grid', origin: [0, 0], dims: [2, 1], cols: 6, want: 0b11 },
      { name: '2x1 at origin [4, 1] on 6-col grid (bits 10, 11)', origin: [4, 1], dims: [2, 1], cols: 6, want: 0b110000000000 },
      { name: '2x2 on 3-col portrait grid (bits 0, 1, 3, 4)', origin: [0, 0], dims: [2, 2], cols: 3, want: 27 },
      // [2, 3] left and [1, 3] right are disjoint (219 & 292 = 0) and fill bits 0..8 (219 | 292 = 511).
      { name: '[2, 3] left half of 3-col portrait grid', origin: [0, 0], dims: [2, 3], cols: 3, want: 219 },
      { name: '[1, 3] right column of 3-col portrait grid', origin: [2, 0], dims: [1, 3], cols: 3, want: 292 },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(computeWidgetBitmask(tc.origin, tc.dims, tc.cols)).toBe(tc.want);
      });
    }
  });

  describe('computeScreenMask and getOccupiedCellCount', () => {
    it('aggregates masks for matching screen and pinned widgets', () => {
      const widgets: LayoutEditorWidget[] = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 1, origin: [2, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'pin', pinned: true, origin: [4, 0], dimensions: [2, 1] }),
      ];

      // Screen 0: w1 (bits 0, 1) + pin (bits 4, 5)
      const mask0 = computeScreenMask(widgets, 0, 6, 2);
      expect(mask0 & 0b11).toBe(0b11);
      expect(mask0 & (0b11 << 4)).toBe(0b11 << 4);
      expect(mask0 & (0b11 << 2)).toBe(0);
      expect(getOccupiedCellCount(widgets, 0, 6, 2)).toBe(4);

      // Screen 1: w2 (bits 2, 3) + pin (bits 4, 5)
      const mask1 = computeScreenMask(widgets, 1, 6, 2);
      expect(mask1 & 0b11).toBe(0);
      expect(mask1 & (0b11 << 2)).toBe(0b11 << 2);
      expect(mask1 & (0b11 << 4)).toBe(0b11 << 4);
      expect(getOccupiedCellCount(widgets, 1, 6, 2)).toBe(4);
    });

    it('excludes widget when excludeWidgetId is supplied', () => {
      const widgets: LayoutEditorWidget[] = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
      ];
      expect(computeScreenMask(widgets, 0, 6, 2, 'w1')).toBe(0);
    });
  });

  describe('getTotalScreens', () => {
    it('returns 1 when no widgets or only pinned widgets exist', () => {
      expect(getTotalScreens([])).toBe(1);
      expect(getTotalScreens([makeWidget({ pinned: true })])).toBe(1);
    });

    it('returns max unpinned screen + 1', () => {
      const widgets = [
        makeWidget({ widget_id: 'a', screen: 0 }),
        makeWidget({ widget_id: 'b', screen: 2 }),
      ];
      expect(getTotalScreens(widgets)).toBe(3);
    });
  });

  describe('canPlaceWidget', () => {
    const one = [makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] })];
    const two = [...one, makeWidget({ widget_id: 'w2', screen: 1, origin: [2, 0], dimensions: [2, 1] })];
    const portrait = [makeWidget({ widget_id: 'photo', screen: 0, origin: [0, 0], dimensions: [2, 3] })];
    type Target = Parameters<typeof canPlaceWidget>[1];
    const at = (origin: [number, number], dimensions: [number, number], screen = 0, pinned = false): Target => ({
      widget_id: 'x',
      screen,
      origin,
      dimensions,
      pinned,
    });
    const tests: { name: string; widgets: LayoutEditorWidget[]; target: Target; cols: number; rows: number; want: boolean }[] = [
      { name: 'rejects negative col', widgets: one, target: at([-1, 0], [2, 1]), cols: 6, rows: 2, want: false },
      { name: 'rejects negative row', widgets: one, target: at([0, -1], [2, 1]), cols: 6, rows: 2, want: false },
      { name: 'rejects width overflow', widgets: one, target: at([5, 0], [2, 1]), cols: 6, rows: 2, want: false },
      { name: 'rejects row past the bottom', widgets: one, target: at([0, 2], [1, 1]), cols: 6, rows: 2, want: false },
      { name: 'rejects zero width', widgets: one, target: at([0, 0], [0, 1]), cols: 6, rows: 2, want: false },
      { name: 'allows free cells on same screen', widgets: one, target: at([2, 0], [2, 1]), cols: 6, rows: 2, want: true },
      { name: 'rejects occupied cells on same screen', widgets: one, target: at([1, 0], [2, 1]), cols: 6, rows: 2, want: false },
      { name: 'allows same cells on a different screen', widgets: one, target: at([0, 0], [2, 1], 1), cols: 6, rows: 2, want: true },
      { name: 'pinned target collides on screen 0', widgets: two, target: at([0, 0], [2, 1], 0, true), cols: 6, rows: 2, want: false },
      { name: 'pinned target collides on screen 1', widgets: two, target: at([2, 0], [2, 1], 0, true), cols: 6, rows: 2, want: false },
      { name: 'pinned target clear on every screen', widgets: two, target: at([4, 0], [2, 1], 0, true), cols: 6, rows: 2, want: true },
      { name: 'portrait [1, 3] beside [2, 3] fits', widgets: portrait, target: at([2, 0], [1, 3]), cols: 3, rows: 6, want: true },
      { name: 'portrait [1, 3] overlapping col 1', widgets: portrait, target: at([1, 0], [1, 3]), cols: 3, rows: 6, want: false },
      { name: 'portrait [2, 3] at col 2 overflows', widgets: portrait, target: at([2, 0], [2, 3]), cols: 3, rows: 6, want: false },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(canPlaceWidget(tc.widgets, tc.target, tc.cols, tc.rows)).toBe(tc.want);
      });
    }
  });

  describe('compactScreens', () => {
    it('compacts non-contiguous screens to 0..K-1', () => {
      const widgets = [
        makeWidget({ widget_id: 'a', screen: 1 }),
        makeWidget({ widget_id: 'b', screen: 3 }),
        makeWidget({ widget_id: 'pin', pinned: true, screen: 2 }),
      ];
      const result = compactScreens(widgets);
      expect(result.find((w) => w.widget_id === 'a')?.screen).toBe(0);
      expect(result.find((w) => w.widget_id === 'b')?.screen).toBe(1);
      expect(result.find((w) => w.widget_id === 'pin')?.screen).toBe(0);
    });

    it('resets screen to 0 when only pinned widgets exist', () => {
      const widgets = [makeWidget({ widget_id: 'pin', pinned: true, screen: 4 })];
      const result = compactScreens(widgets);
      expect(itemAt(result, 0).screen).toBe(0);
    });
  });

  describe('moveOrSwapWidget', () => {
    const pinFirst = (...rest: LayoutEditorWidget[]) => [
      makeWidget({ widget_id: 'pin', pinned: true, screen: 0, origin: [0, 0], dimensions: [2, 1] }),
      ...rest,
    ];
    const u1 = makeWidget({ widget_id: 'u1', screen: 1, origin: [2, 0], dimensions: [2, 1] });
    const unchanged: { name: string; widgets: LayoutEditorWidget[]; id: string; screen: number; dest: [number, number] }[] = [
      { name: 'unknown widget', widgets: [makeWidget()], id: 'unknown', screen: 0, dest: [2, 0] },
      { name: 'identical destination', widgets: [makeWidget()], id: 'w1', screen: 0, dest: [0, 0] },
      {
        name: 'pinned dropped on its own origin on activeScreen > 0',
        widgets: [makeWidget({ widget_id: 'pin', pinned: true, origin: [2, 0] })],
        id: 'pin',
        screen: 1,
        dest: [2, 0],
      },
      { name: 'destination overflows the right edge', widgets: [makeWidget()], id: 'w1', screen: 0, dest: [5, 0] },
      { name: 'destination left of the grid', widgets: [makeWidget()], id: 'w1', screen: 0, dest: [-1, 0] },
      {
        // On screen 1, pin at [2, 0] would collide with u0 on screen 0.
        name: 'pinned swap that collides on another screen',
        widgets: pinFirst(makeWidget({ widget_id: 'u0', screen: 0, origin: [2, 0], dimensions: [2, 1] }), u1),
        id: 'pin',
        screen: 1,
        dest: [2, 0],
      },
      { name: 'swap between pinned and unpinned across different screens', widgets: pinFirst(u1), id: 'u1', screen: 0, dest: [0, 0] },
      {
        // pin (width 2) at w1's origin [5, 0] overflows, and pinned widgets are not reflowed.
        name: 'swap that puts the pinned widget out of bounds at the source origin',
        widgets: [makeWidget({ origin: [5, 0], dimensions: [1, 1] }), ...pinFirst()],
        id: 'w1',
        screen: 0,
        dest: [0, 0],
      },
      {
        // pin at [0, 0] would cover [0..1, 0] and still overlap w1 at [1, 0].
        name: 'swap whose swapped positions still collide with a pinned widget',
        widgets: [
          makeWidget({ origin: [0, 0], dimensions: [1, 1] }),
          makeWidget({ widget_id: 'pin', pinned: true, origin: [1, 0], dimensions: [2, 1] }),
        ],
        id: 'w1',
        screen: 0,
        dest: [1, 0],
      },
    ];
    for (const tc of unchanged) {
      it(`returns unchanged: ${tc.name}`, () => {
        expect(moveOrSwapWidget(tc.widgets, tc.id, tc.screen, tc.dest, 6, 2)).toEqual(tc.widgets);
      });
    }

    it('moves into free space directly', () => {
      const widgets = [makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] })];
      const res = moveOrSwapWidget(widgets, 'w1', 0, [2, 0], 6, 2);
      expect(itemAt(res, 0).origin).toEqual([2, 0]);
    });

    it('moves pinned widget into free space on targetScreen > 0 and preserves screen: 0', () => {
      const widgets = [
        makeWidget({ widget_id: 'pin', pinned: true, screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'u1', pinned: false, screen: 1, origin: [4, 0], dimensions: [2, 1] }),
      ];
      // Drag pin to [2, 0] on screen 1: free on both screens
      const res = moveOrSwapWidget(widgets, 'pin', 1, [2, 0], 6, 2);
      const pin = res.find((w) => w.widget_id === 'pin');
      expect(pin?.origin).toEqual([2, 0]);
      expect(pin?.screen).toBe(0);
      expect(pin?.pinned).toBe(true);
    });

    it('swaps with single overlapping widget if dimensions fit', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 0, origin: [2, 0], dimensions: [2, 1] }),
      ];
      // Move w1 onto w2 at [2, 0] -> swaps positions
      const res = moveOrSwapWidget(widgets, 'w1', 0, [2, 0], 6, 2);
      expect(res.find((w) => w.widget_id === 'w1')?.origin).toEqual([2, 0]);
      expect(res.find((w) => w.widget_id === 'w2')?.origin).toEqual([0, 0]);
    });

    it('swaps pinned widget onto unpinned widget on targetScreen > 0', () => {
      const widgets = [
        makeWidget({ widget_id: 'pin', pinned: true, screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'u1', pinned: false, screen: 1, origin: [2, 0], dimensions: [2, 1] }),
      ];
      // Drag pin onto u1 at [2, 0] on screen 1
      const res = moveOrSwapWidget(widgets, 'pin', 1, [2, 0], 6, 2);
      const pin = res.find((w) => w.widget_id === 'pin');
      const u1 = res.find((w) => w.widget_id === 'u1');
      expect(pin?.origin).toEqual([2, 0]);
      expect(pin?.screen).toBe(0);
      expect(u1?.origin).toEqual([0, 0]);
      expect(u1?.screen).toBe(1);
    });

    it('swaps unpinned widget onto pinned widget on targetScreen > 0', () => {
      const widgets = [
        makeWidget({ widget_id: 'pin', pinned: true, screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'u1', pinned: false, screen: 1, origin: [2, 0], dimensions: [2, 1] }),
      ];
      // Drag u1 onto pin at [0, 0] on screen 1
      const res = moveOrSwapWidget(widgets, 'u1', 1, [0, 0], 6, 2);
      const pin = res.find((w) => w.widget_id === 'pin');
      const u1 = res.find((w) => w.widget_id === 'u1');
      expect(u1?.origin).toEqual([0, 0]);
      expect(u1?.screen).toBe(1);
      expect(pin?.origin).toEqual([2, 0]);
      expect(pin?.screen).toBe(0);
    });

    it('swaps and reflows when overlapping with multiple widgets', () => {
      const widgets = [
        makeWidget({ widget_id: 'big', screen: 0, origin: [0, 0], dimensions: [4, 1] }),
        makeWidget({ widget_id: 's1', screen: 0, origin: [0, 1], dimensions: [2, 1] }),
        makeWidget({ widget_id: 's2', screen: 0, origin: [2, 1], dimensions: [2, 1] }),
      ];
      // Drag big onto [0, 1] overlaps both s1 and s2 -> reflows s1 and s2 into row 0
      const res = moveOrSwapWidget(widgets, 'big', 0, [0, 1], 6, 2);
      expect(res.find((w) => w.widget_id === 'big')?.origin).toEqual([0, 1]);
      expect(res.find((w) => w.widget_id === 's1')?.origin).toEqual([0, 0]);
      expect(res.find((w) => w.widget_id === 's2')?.origin).toEqual([2, 0]);
    });

    it('swaps 4x2 family-calendar on left with two 2x1 widgets on right (including intermediate col 1)', () => {
      const widgets = [
        makeWidget({ widget_id: 'family-calendar', screen: 0, origin: [0, 0], dimensions: [4, 2] }),
        makeWidget({ widget_id: 'home-weather', screen: 0, origin: [4, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'family-tasks', screen: 0, origin: [4, 1], dimensions: [2, 1] }),
      ];

      // Drop directly on [2, 0]
      const resDirect = moveOrSwapWidget(widgets, 'family-calendar', 0, [2, 0], 6, 2);
      expect(resDirect.find((w) => w.widget_id === 'family-calendar')?.origin).toEqual([2, 0]);
      expect(resDirect.find((w) => w.widget_id === 'home-weather')?.origin).toEqual([0, 0]);
      expect(resDirect.find((w) => w.widget_id === 'family-tasks')?.origin).toEqual([0, 1]);

      // Drop on intermediate column [1, 0] snaps to [2, 0]
      const resSnap = moveOrSwapWidget(widgets, 'family-calendar', 0, [1, 0], 6, 2);
      expect(resSnap.find((w) => w.widget_id === 'family-calendar')?.origin).toEqual([2, 0]);
      expect(resSnap.find((w) => w.widget_id === 'home-weather')?.origin).toEqual([0, 0]);
      expect(resSnap.find((w) => w.widget_id === 'family-tasks')?.origin).toEqual([0, 1]);

      // Dragging back from [2, 0] to [0, 0] or [1, 0] restores original positions
      const resBack = moveOrSwapWidget(resDirect, 'family-calendar', 0, [1, 0], 6, 2);
      expect(resBack.find((w) => w.widget_id === 'family-calendar')?.origin).toEqual([0, 0]);
      expect(resBack.find((w) => w.widget_id === 'home-weather')?.origin).toEqual([4, 0]);
      expect(resBack.find((w) => w.widget_id === 'family-tasks')?.origin).toEqual([4, 1]);
    });

    it('reflows screen when dragging a 2x1 widget onto a 4x2 widget', () => {
      const widgets = [
        makeWidget({ widget_id: 'family-calendar', screen: 0, origin: [0, 0], dimensions: [4, 2] }),
        makeWidget({ widget_id: 'home-weather', screen: 0, origin: [4, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'family-tasks', screen: 0, origin: [4, 1], dimensions: [2, 1] }),
      ];

      const res = moveOrSwapWidget(widgets, 'home-weather', 0, [0, 0], 6, 2);
      expect(res.find((w) => w.widget_id === 'home-weather')?.origin).toEqual([0, 0]);
      expect(res.find((w) => w.widget_id === 'family-tasks')?.origin).toEqual([0, 1]);
      expect(res.find((w) => w.widget_id === 'family-calendar')?.origin).toEqual([2, 0]);
    });

    it('reflows [2, R] photo-carousel beside [1, R] tasks on 3x6 portrait grid', () => {
      // Photo [2, 3] on left (cols 0, 1), tasks [1, 3] on right (col 2)
      const widgets = [
        makeWidget({ widget_id: 'photo', screen: 0, origin: [0, 0], dimensions: [2, 3] }),
        makeWidget({ widget_id: 'tasks', screen: 0, origin: [2, 0], dimensions: [1, 3] }),
      ];

      // Dragging photo [2, 3] to [1, 0] swaps tasks into vacated column 0
      const resMovePhoto = moveOrSwapWidget(widgets, 'photo', 0, [1, 0], 3, 6);
      expect(resMovePhoto.find((w) => w.widget_id === 'photo')?.origin).toEqual([1, 0]);
      expect(resMovePhoto.find((w) => w.widget_id === 'tasks')?.origin).toEqual([0, 0]);

      // Dragging tasks [1, 3] from [2, 0] to [0, 0] reflows photo [2, 3] into columns 1..2
      const resMoveTasks = moveOrSwapWidget(widgets, 'tasks', 0, [0, 0], 3, 6);
      expect(resMoveTasks.find((w) => w.widget_id === 'tasks')?.origin).toEqual([0, 0]);
      expect(resMoveTasks.find((w) => w.widget_id === 'photo')?.origin).toEqual([1, 0]);
    });

    it('resolves preview origin to snapped landing slot', () => {
      const widgets = [
        makeWidget({ widget_id: 'family-calendar', screen: 0, origin: [0, 0], dimensions: [4, 2] }),
        makeWidget({ widget_id: 'home-weather', screen: 0, origin: [4, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'family-tasks', screen: 0, origin: [4, 1], dimensions: [2, 1] }),
      ];
      expect(resolvePreviewOrigin(widgets, 'family-calendar', 0, [1, 0], 6, 2)).toEqual([2, 0]);
      expect(resolvePreviewOrigin(widgets, 'unknown', 0, [1, 0], 6, 2)).toEqual([1, 0]);
    });

  });

  describe('moveWidgetToScreen', () => {
    const unchanged: { name: string; widgets: LayoutEditorWidget[]; id: string; screen: number }[] = [
      { name: 'unknown id', widgets: [makeWidget()], id: 'unknown', screen: 1 },
      { name: 'negative target screen', widgets: [makeWidget()], id: 'w1', screen: -1 },
      { name: 'already on target screen and unpinned', widgets: [makeWidget()], id: 'w1', screen: 0 },
      {
        name: 'target screen is 100% full',
        widgets: [
          makeWidget(),
          makeWidget({ widget_id: 'full1', screen: 1, origin: [0, 0], dimensions: [6, 1] }),
          makeWidget({ widget_id: 'full2', screen: 1, origin: [0, 1], dimensions: [6, 1] }),
        ],
        id: 'w1',
        screen: 1,
      },
    ];
    for (const tc of unchanged) {
      it(`returns unchanged: ${tc.name}`, () => {
        expect(moveWidgetToScreen(tc.widgets, tc.id, tc.screen, 6, 2)).toEqual(tc.widgets);
      });
    }

    it('places at current origin on target screen when free', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [2, 1], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 1, origin: [0, 0], dimensions: [2, 1] }),
      ];
      const res = moveWidgetToScreen(widgets, 'w1', 1, 6, 2);
      const w1 = res.find((w) => w.widget_id === 'w1');
      expect(w1?.screen).toBe(0); // Screen 0 became empty, screen 1 compacted to 0
      expect(w1?.origin).toEqual([2, 1]);
    });

    it('finds open slot when current origin is occupied on target screen', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 1, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w3', screen: 0, origin: [2, 0], dimensions: [2, 1] }),
      ];
      const res = moveWidgetToScreen(widgets, 'w1', 1, 6, 2);
      const w1 = res.find((w) => w.widget_id === 'w1');
      expect(w1?.screen).toBe(1);
      expect(w1?.origin).toEqual([2, 0]); // Next open slot
    });

  });

  describe('resizeWidget', () => {
    // Fills every cell except w1's two, so nothing larger than [2, 1] fits anywhere.
    const packed = [
      makeWidget(),
      makeWidget({ widget_id: 'b1', origin: [2, 0], dimensions: [4, 1] }),
      makeWidget({ widget_id: 'b2', origin: [0, 1], dimensions: [6, 1] }),
    ];
    const unchanged: { name: string; widgets: LayoutEditorWidget[]; id: string; dims: [number, number] }[] = [
      { name: 'unknown id', widgets: [makeWidget()], id: 'unknown', dims: [3, 2] },
      { name: 'identical dimensions', widgets: [makeWidget()], id: 'w1', dims: [2, 1] },
      { name: 'zero width', widgets: [makeWidget()], id: 'w1', dims: [0, 1] },
      { name: 'wider than the grid', widgets: [makeWidget()], id: 'w1', dims: [7, 1] },
      { name: 'taller than the grid', widgets: [makeWidget()], id: 'w1', dims: [2, 3] },
      { name: 'no slot fits the new dimensions', widgets: packed, id: 'w1', dims: [4, 2] },
    ];
    for (const tc of unchanged) {
      it(`returns unchanged: ${tc.name}`, () => {
        expect(resizeWidget(tc.widgets, tc.id, tc.dims, 6, 2)).toEqual(tc.widgets);
      });
    }

    it('resizes in place if current origin fits new dimensions', () => {
      const widgets = [makeWidget({ widget_id: 'w1', origin: [0, 0], dimensions: [2, 1] })];
      const res = resizeWidget(widgets, 'w1', [3, 2], 6, 2);
      expect(itemAt(res, 0).dimensions).toEqual([3, 2]);
      expect(itemAt(res, 0).origin).toEqual([0, 0]);
    });

    it('clamps origin towards left/top when expanding past grid boundary', () => {
      // At col 5, expanding to width 2 overflows 5+2=7 > 6. Clamped col should be 4.
      const widgets = [makeWidget({ widget_id: 'w1', origin: [5, 0], dimensions: [1, 1] })];
      const res = resizeWidget(widgets, 'w1', [2, 1], 6, 2);
      expect(itemAt(res, 0).dimensions).toEqual([2, 1]);
      expect(itemAt(res, 0).origin).toEqual([4, 0]);
    });

    it('searches for open slot if clamped origin is blocked', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', origin: [5, 0], dimensions: [1, 1] }),
        makeWidget({ widget_id: 'blocker', origin: [3, 0], dimensions: [2, 1] }),
      ];
      // Clamped col 4 is blocked by blocker (cols 3, 4). Next open slot is row 1 [0, 1]
      const res = resizeWidget(widgets, 'w1', [2, 1], 6, 2);
      expect(res.find((w) => w.widget_id === 'w1')?.origin).toEqual([0, 0]); // Col 0 is open!
    });

  });

  describe('togglePinWidget', () => {
    const unchanged: { name: string; widgets: LayoutEditorWidget[]; id: string }[] = [
      { name: 'unknown id', widgets: [makeWidget()], id: 'unknown' },
      {
        // w1 at [0, 0] collides with w2 on screen 1.
        name: 'pinning collides on another screen',
        widgets: [makeWidget(), makeWidget({ widget_id: 'w2', screen: 1, origin: [0, 0], dimensions: [2, 1] })],
        id: 'w1',
      },
    ];
    for (const tc of unchanged) {
      it(`returns unchanged: ${tc.name}`, () => {
        expect(togglePinWidget(tc.widgets, tc.id, 6, 2)).toEqual(tc.widgets);
      });
    }

    it('unpins a currently pinned widget onto the active screen', () => {
      const widgets = [
        makeWidget({ widget_id: 'u0', pinned: false, screen: 0, origin: [0, 0] }),
        makeWidget({ widget_id: 'u1', pinned: false, screen: 1, origin: [0, 0] }),
        makeWidget({ widget_id: 'w1', pinned: true, screen: 0, origin: [4, 0] }),
      ];
      const res = togglePinWidget(widgets, 'w1', 6, 2, 1);
      const w1 = res.find((w) => w.widget_id === 'w1');
      expect(w1?.pinned).toBe(false);
      expect(w1?.screen).toBe(1);
    });

    it('compacts screens when pinning the last unpinned widget on an earlier screen', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [4, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 1, origin: [0, 0], dimensions: [2, 1] }),
      ];
      const res = togglePinWidget(widgets, 'w1', 6, 2, 0);
      expect(res.find((w) => w.widget_id === 'w1')?.pinned).toBe(true);
      expect(res.find((w) => w.widget_id === 'w2')?.screen).toBe(0);
    });

    it('pins an unpinned widget when position is clear on all screens', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [4, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 1, origin: [0, 0], dimensions: [2, 1] }),
      ];
      const res = togglePinWidget(widgets, 'w1', 6, 2);
      const w1 = res.find((w) => w.widget_id === 'w1');
      expect(w1?.pinned).toBe(true);
      expect(w1?.screen).toBe(0);
    });

  });
});
