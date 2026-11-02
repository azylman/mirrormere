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
  togglePinWidget,
} from './editorMath';

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
    it('computes 1x1 mask at origin [0, 0]', () => {
      expect(computeWidgetBitmask([0, 0], [1, 1], 6)).toBe(1);
    });

    it('computes 2x1 mask at origin [0, 0] on 6-col grid', () => {
      expect(computeWidgetBitmask([0, 0], [2, 1], 6)).toBe(0b000000000011);
    });

    it('computes 2x1 mask at origin [4, 1] on 6-col grid', () => {
      // Row 1 starts at bit 6. Col 4 is bit 10, Col 5 is bit 11.
      expect(computeWidgetBitmask([4, 1], [2, 1], 6)).toBe((0b11 << 4) << 6);
    });

    it('computes 2x2 mask on 2x6 portrait grid', () => {
      // 2 cols, row 0 and row 1 = bits 0, 1, 2, 3 -> 0b1111 = 15
      expect(computeWidgetBitmask([0, 0], [2, 2], 2)).toBe(15);
    });
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
    const widgets: LayoutEditorWidget[] = [
      makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
    ];

    it('rejects out of bounds origins and dimensions', () => {
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 0, origin: [-1, 0], dimensions: [2, 1] }, 6, 2)).toBe(false);
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 0, origin: [0, -1], dimensions: [2, 1] }, 6, 2)).toBe(false);
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 0, origin: [5, 0], dimensions: [2, 1] }, 6, 2)).toBe(false);
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 0, origin: [0, 2], dimensions: [1, 1] }, 6, 2)).toBe(false);
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 0, origin: [0, 0], dimensions: [0, 1] }, 6, 2)).toBe(false);
    });

    it('allows placement in free cells on same screen', () => {
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 0, origin: [2, 0], dimensions: [2, 1] }, 6, 2)).toBe(true);
    });

    it('rejects placement in occupied cells on same screen', () => {
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 0, origin: [1, 0], dimensions: [2, 1] }, 6, 2)).toBe(false);
    });

    it('allows placement in same cells on different screen', () => {
      expect(canPlaceWidget(widgets, { widget_id: 'x', screen: 1, origin: [0, 0], dimensions: [2, 1] }, 6, 2)).toBe(true);
    });

    it('handles pinned target checking all screens', () => {
      const multi = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 1, origin: [2, 0], dimensions: [2, 1] }),
      ];
      // Origin [0, 0] collides on screen 0
      expect(canPlaceWidget(multi, { widget_id: 'pin', screen: 0, origin: [0, 0], dimensions: [2, 1], pinned: true }, 6, 2)).toBe(false);
      // Origin [2, 0] collides on screen 1
      expect(canPlaceWidget(multi, { widget_id: 'pin', screen: 0, origin: [2, 0], dimensions: [2, 1], pinned: true }, 6, 2)).toBe(false);
      // Origin [4, 0] is clear on both screens
      expect(canPlaceWidget(multi, { widget_id: 'pin', screen: 0, origin: [4, 0], dimensions: [2, 1], pinned: true }, 6, 2)).toBe(true);
    });
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
      expect(result[0].screen).toBe(0);
    });
  });

  describe('moveOrSwapWidget', () => {
    it('returns unchanged if widget is not found', () => {
      const widgets = [makeWidget({ widget_id: 'w1' })];
      expect(moveOrSwapWidget(widgets, 'unknown', 0, [2, 0], 6, 2)).toEqual(widgets);
    });

    it('returns unchanged if destination is identical', () => {
      const widgets = [makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0] })];
      expect(moveOrSwapWidget(widgets, 'w1', 0, [0, 0], 6, 2)).toEqual(widgets);
    });

    it('returns unchanged if pinned widget is dropped on same origin on activeScreen > 0', () => {
      const widgets = [makeWidget({ widget_id: 'pin', pinned: true, screen: 0, origin: [2, 0] })];
      expect(moveOrSwapWidget(widgets, 'pin', 1, [2, 0], 6, 2)).toEqual(widgets);
    });

    it('returns unchanged if destination is out of bounds', () => {
      const widgets = [makeWidget({ widget_id: 'w1', origin: [0, 0], dimensions: [2, 1] })];
      expect(moveOrSwapWidget(widgets, 'w1', 0, [5, 0], 6, 2)).toEqual(widgets);
      expect(moveOrSwapWidget(widgets, 'w1', 0, [-1, 0], 6, 2)).toEqual(widgets);
    });

    it('moves into free space directly', () => {
      const widgets = [makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] })];
      const res = moveOrSwapWidget(widgets, 'w1', 0, [2, 0], 6, 2);
      expect(res[0].origin).toEqual([2, 0]);
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

    it('rejects swap between pinned and unpinned if pinned widget collides on another screen', () => {
      const widgets = [
        makeWidget({ widget_id: 'pin', pinned: true, screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'u0', pinned: false, screen: 0, origin: [2, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'u1', pinned: false, screen: 1, origin: [2, 0], dimensions: [2, 1] }),
      ];
      // On screen 1, dragging pin to [2, 0] would put pin at [2, 0], colliding with u0 on screen 0
      const res = moveOrSwapWidget(widgets, 'pin', 1, [2, 0], 6, 2);
      expect(res).toEqual(widgets);
    });

    it('rejects swap if overlapping with more than one widget', () => {
      const widgets = [
        makeWidget({ widget_id: 'big', screen: 0, origin: [0, 0], dimensions: [4, 1] }),
        makeWidget({ widget_id: 's1', screen: 0, origin: [0, 1], dimensions: [2, 1] }),
        makeWidget({ widget_id: 's2', screen: 0, origin: [2, 1], dimensions: [2, 1] }),
      ];
      // Drag big onto [0, 1] overlaps both s1 and s2
      const res = moveOrSwapWidget(widgets, 'big', 0, [0, 1], 6, 2);
      expect(res).toEqual(widgets);
    });

    it('rejects swap between pinned and unpinned across different screens', () => {
      const widgets = [
        makeWidget({ widget_id: 'pin', pinned: true, screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'u1', pinned: false, screen: 1, origin: [2, 0], dimensions: [2, 1] }),
      ];
      const res = moveOrSwapWidget(widgets, 'u1', 0, [0, 0], 6, 2);
      expect(res).toEqual(widgets);
    });

    it('rejects swap if other widget would be out of bounds at source origin', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [5, 0], dimensions: [1, 1] }),
        makeWidget({ widget_id: 'w2', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
      ];
      // w2 has width 2, at origin [5, 0] 5+2=7 > 6
      const res = moveOrSwapWidget(widgets, 'w1', 0, [0, 0], 6, 2);
      expect(res).toEqual(widgets);
    });

    it('rejects swap if swapped positions still collide with each other', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [1, 1] }),
        makeWidget({ widget_id: 'w2', screen: 0, origin: [1, 0], dimensions: [2, 1] }),
      ];
      // Moving w1 to [1, 0] overlaps w2. Putting w2 at [0, 0] means w2 occupies [0..1, 0] and w1 occupies [1, 0].
      const res = moveOrSwapWidget(widgets, 'w1', 0, [1, 0], 6, 2);
      expect(res).toEqual(widgets);
    });
  });

  describe('moveWidgetToScreen', () => {
    it('returns unchanged for unknown id or negative target screen', () => {
      const widgets = [makeWidget({ widget_id: 'w1' })];
      expect(moveWidgetToScreen(widgets, 'unknown', 1, 6, 2)).toEqual(widgets);
      expect(moveWidgetToScreen(widgets, 'w1', -1, 6, 2)).toEqual(widgets);
    });

    it('returns unchanged if already on target screen and unpinned', () => {
      const widgets = [makeWidget({ widget_id: 'w1', screen: 0, pinned: false })];
      expect(moveWidgetToScreen(widgets, 'w1', 0, 6, 2)).toEqual(widgets);
    });

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

    it('returns unchanged if target screen is 100% full', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'full1', screen: 1, origin: [0, 0], dimensions: [6, 1] }),
        makeWidget({ widget_id: 'full2', screen: 1, origin: [0, 1], dimensions: [6, 1] }),
      ];
      expect(moveWidgetToScreen(widgets, 'w1', 1, 6, 2)).toEqual(widgets);
    });
  });

  describe('resizeWidget', () => {
    it('returns unchanged for unknown id, identical dimensions, or invalid bounds', () => {
      const widgets = [makeWidget({ widget_id: 'w1', dimensions: [2, 1] })];
      expect(resizeWidget(widgets, 'unknown', [3, 2], 6, 2)).toEqual(widgets);
      expect(resizeWidget(widgets, 'w1', [2, 1], 6, 2)).toEqual(widgets);
      expect(resizeWidget(widgets, 'w1', [0, 1], 6, 2)).toEqual(widgets);
      expect(resizeWidget(widgets, 'w1', [7, 1], 6, 2)).toEqual(widgets);
      expect(resizeWidget(widgets, 'w1', [2, 3], 6, 2)).toEqual(widgets);
    });

    it('resizes in place if current origin fits new dimensions', () => {
      const widgets = [makeWidget({ widget_id: 'w1', origin: [0, 0], dimensions: [2, 1] })];
      const res = resizeWidget(widgets, 'w1', [3, 2], 6, 2);
      expect(res[0].dimensions).toEqual([3, 2]);
      expect(res[0].origin).toEqual([0, 0]);
    });

    it('clamps origin towards left/top when expanding past grid boundary', () => {
      // At col 5, expanding to width 2 overflows 5+2=7 > 6. Clamped col should be 4.
      const widgets = [makeWidget({ widget_id: 'w1', origin: [5, 0], dimensions: [1, 1] })];
      const res = resizeWidget(widgets, 'w1', [2, 1], 6, 2);
      expect(res[0].dimensions).toEqual([2, 1]);
      expect(res[0].origin).toEqual([4, 0]);
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

    it('returns unchanged if no slot fits new dimensions', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'b1', origin: [2, 0], dimensions: [4, 1] }),
        makeWidget({ widget_id: 'b2', origin: [0, 1], dimensions: [6, 1] }),
      ];
      // Trying to expand to [4, 2] needs 8 cells, only 2 open cells exist
      expect(resizeWidget(widgets, 'w1', [4, 2], 6, 2)).toEqual(widgets);
    });
  });

  describe('togglePinWidget', () => {
    it('returns unchanged for unknown id', () => {
      const widgets = [makeWidget({ widget_id: 'w1' })];
      expect(togglePinWidget(widgets, 'unknown', 6, 2)).toEqual(widgets);
    });

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

    it('rejects pinning when position collides on another screen', () => {
      const widgets = [
        makeWidget({ widget_id: 'w1', screen: 0, origin: [0, 0], dimensions: [2, 1] }),
        makeWidget({ widget_id: 'w2', screen: 1, origin: [0, 0], dimensions: [2, 1] }),
      ];
      // w1 at [0, 0] collides with w2 on screen 1
      expect(togglePinWidget(widgets, 'w1', 6, 2)).toEqual(widgets);
    });
  });
});
