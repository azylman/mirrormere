import type { LayoutEditorWidget } from '../sse/types';

/**
 * Computes the 12-bit integer cell bitmask for a rectangular placement on a gridCols-wide grid.
 */
export function computeWidgetBitmask(
  origin: [number, number],
  dimensions: [number, number],
  gridCols: number,
): number {
  let mask = 0;
  const [col, row] = origin;
  const [cols, rows] = dimensions;
  for (let r = 0; r < rows; r++) {
    const rowMask = ((1 << cols) - 1) << col;
    mask |= rowMask << ((row + r) * gridCols);
  }
  return mask;
}

/**
 * Computes the aggregate cell bitmask for all placed or pinned widgets on `screen`.
 */
export function computeScreenMask(
  widgets: readonly LayoutEditorWidget[],
  screen: number,
  gridCols: number,
  _gridRows: number,
  excludeWidgetId?: string,
): number {
  let mask = 0;
  for (const w of widgets) {
    if (excludeWidgetId && w.widget_id === excludeWidgetId) {
      continue;
    }
    if (w.pinned || w.screen === screen) {
      mask |= computeWidgetBitmask(w.origin, w.dimensions, gridCols);
    }
  }
  return mask;
}

/**
 * Returns the total number of rotation screens represented by unpinned widgets.
 */
export function getTotalScreens(widgets: readonly LayoutEditorWidget[]): number {
  const unpinned = widgets.filter((w) => !w.pinned);
  if (unpinned.length === 0) return 1;
  return Math.max(1, Math.max(...unpinned.map((w) => w.screen)) + 1);
}

/**
 * Computes the number of non-empty cells occupied on `screen`.
 */
export function getOccupiedCellCount(
  widgets: readonly LayoutEditorWidget[],
  screen: number,
  gridCols: number,
  gridRows: number,
): number {
  const mask = computeScreenMask(widgets, screen, gridCols, gridRows);
  let count = 0;
  let m = mask;
  while (m > 0) {
    count += m & 1;
    m >>= 1;
  }
  return count;
}

/**
 * Validates grid boundaries and checks for bitmask collisions on target screen(s).
 */
export function canPlaceWidget(
  widgets: readonly LayoutEditorWidget[],
  target: {
    widget_id: string;
    screen: number;
    origin: [number, number];
    dimensions: [number, number];
    pinned?: boolean;
  },
  gridCols: number,
  gridRows: number,
): boolean {
  const [col, row] = target.origin;
  const [cols, rows] = target.dimensions;
  if (col < 0 || row < 0 || cols < 1 || rows < 1) {
    return false;
  }
  if (col + cols > gridCols || row + rows > gridRows) {
    return false;
  }
  const targetMask = computeWidgetBitmask(target.origin, target.dimensions, gridCols);
  if (target.pinned) {
    const unpinnedScreens = widgets
      .filter((w) => !w.pinned && w.widget_id !== target.widget_id)
      .map((w) => w.screen);
    const maxS = unpinnedScreens.length === 0 ? 1 : Math.max(1, Math.max(...unpinnedScreens) + 1);
    for (let s = 0; s < maxS; s++) {
      const sMask = computeScreenMask(widgets, s, gridCols, gridRows, target.widget_id);
      if ((sMask & targetMask) !== 0) {
        return false;
      }
    }
    return true;
  }
  const sMask = computeScreenMask(widgets, target.screen, gridCols, gridRows, target.widget_id);
  return (sMask & targetMask) === 0;
}

/**
 * Re-indexes screen indices for unpinned widgets so they form a contiguous 0..K-1 range.
 */
export function compactScreens(widgets: readonly LayoutEditorWidget[]): LayoutEditorWidget[] {
  const unpinned = widgets.filter((w) => !w.pinned);
  if (unpinned.length === 0) {
    return widgets.map((w) => (w.screen !== 0 ? { ...w, screen: 0 } : w));
  }
  const screenSet = new Set<number>();
  for (const w of unpinned) {
    screenSet.add(w.screen);
  }
  const sortedScreens = Array.from(screenSet).sort((a, b) => a - b);
  const screenMap = new Map<number, number>();
  sortedScreens.forEach((oldScreen, newScreen) => {
    screenMap.set(oldScreen, newScreen);
  });
  return widgets.map((w) => {
    if (w.pinned) {
      return w.screen === 0 ? w : { ...w, screen: 0 };
    }
    const newScreen = screenMap.get(w.screen) ?? 0;
    return w.screen === newScreen ? w : { ...w, screen: newScreen };
  });
}

function verifyNoCollisions(
  widgets: readonly LayoutEditorWidget[],
  gridCols: number,
): boolean {
  const maxS = getTotalScreens(widgets);
  for (let s = 0; s < maxS; s++) {
    let sMask = 0;
    for (const w of widgets) {
      if (w.pinned || w.screen === s) {
        const m = computeWidgetBitmask(w.origin, w.dimensions, gridCols);
        if ((sMask & m) !== 0) {
          return false;
        }
        sMask |= m;
      }
    }
  }
  return true;
}

/**
 * Moves a widget to a target origin on targetScreen, or swaps with the single overlapping widget if possible.
 */
export function moveOrSwapWidget(
  widgets: readonly LayoutEditorWidget[],
  widgetId: string,
  targetScreen: number,
  targetOrigin: [number, number],
  gridCols: number,
  gridRows: number,
): LayoutEditorWidget[] {
  const source = widgets.find((w) => w.widget_id === widgetId);
  if (!source) return [...widgets];

  if (
    (source.pinned || source.screen === targetScreen) &&
    source.origin[0] === targetOrigin[0] &&
    source.origin[1] === targetOrigin[1]
  ) {
    return [...widgets];
  }

  const [tCol, tRow] = targetOrigin;
  if (
    tCol < 0 ||
    tRow < 0 ||
    tCol + source.dimensions[0] > gridCols ||
    tRow + source.dimensions[1] > gridRows
  ) {
    return [...widgets];
  }

  // Try direct non-overlapping move (pinned widgets retain screen: 0)
  const targetScreenActual = source.pinned ? 0 : targetScreen;
  if (
    canPlaceWidget(
      widgets,
      { ...source, screen: targetScreenActual, origin: targetOrigin },
      gridCols,
      gridRows,
    )
  ) {
    return widgets.map((w) =>
      w.widget_id === widgetId ? { ...w, screen: targetScreenActual, origin: targetOrigin } : w,
    );
  }

  // Check if swapping with single overlapping widget is viable
  const targetMask = computeWidgetBitmask(targetOrigin, source.dimensions, gridCols);
  const overlapping = widgets.filter(
    (w) =>
      w.widget_id !== widgetId &&
      (w.pinned || w.screen === targetScreen) &&
      (computeWidgetBitmask(w.origin, w.dimensions, gridCols) & targetMask) !== 0,
  );

  if (overlapping.length !== 1) {
    return [...widgets];
  }

  const other = overlapping[0];
  if (!source.pinned && source.screen !== targetScreen) {
    return [...widgets];
  }

  if (
    source.origin[0] < 0 ||
    source.origin[1] < 0 ||
    source.origin[0] + other.dimensions[0] > gridCols ||
    source.origin[1] + other.dimensions[1] > gridRows
  ) {
    return [...widgets];
  }

  const candidate = widgets.map((w) => {
    if (w.widget_id === widgetId) {
      return { ...w, screen: source.pinned ? 0 : targetScreen, origin: targetOrigin };
    }
    if (w.widget_id === other.widget_id) {
      return { ...w, screen: other.pinned ? 0 : targetScreen, origin: source.origin };
    }
    return w;
  });

  if (!verifyNoCollisions(candidate, gridCols)) {
    return [...widgets];
  }

  return candidate;
}

/**
 * Moves a widget to targetScreen, finding an open placement and compacting empty screens.
 */
export function moveWidgetToScreen(
  widgets: readonly LayoutEditorWidget[],
  widgetId: string,
  targetScreen: number,
  gridCols: number,
  gridRows: number,
): LayoutEditorWidget[] {
  const widget = widgets.find((w) => w.widget_id === widgetId);
  if (!widget || targetScreen < 0) return [...widgets];
  if (widget.screen === targetScreen && !widget.pinned) return [...widgets];

  let placed: LayoutEditorWidget | null = null;
  if (
    canPlaceWidget(
      widgets,
      { ...widget, screen: targetScreen, origin: widget.origin, pinned: false },
      gridCols,
      gridRows,
    )
  ) {
    placed = { ...widget, screen: targetScreen, origin: widget.origin, pinned: false };
  } else {
    for (let r = 0; r <= gridRows - widget.dimensions[1]; r++) {
      for (let c = 0; c <= gridCols - widget.dimensions[0]; c++) {
        if (
          canPlaceWidget(
            widgets,
            { ...widget, screen: targetScreen, origin: [c, r], pinned: false },
            gridCols,
            gridRows,
          )
        ) {
          placed = { ...widget, screen: targetScreen, origin: [c, r], pinned: false };
          break;
        }
      }
      if (placed) break;
    }
  }

  if (!placed) return [...widgets];
  const updated = widgets.map((w) => (w.widget_id === widgetId ? placed! : w));
  return compactScreens(updated);
}

/**
 * Resizes a widget to newDimensions, adjusting origin or relocating to an open slot if needed.
 */
export function resizeWidget(
  widgets: readonly LayoutEditorWidget[],
  widgetId: string,
  newDimensions: [number, number],
  gridCols: number,
  gridRows: number,
): LayoutEditorWidget[] {
  const widget = widgets.find((w) => w.widget_id === widgetId);
  if (!widget) return [...widgets];
  const [newCols, newRows] = newDimensions;
  if (widget.dimensions[0] === newCols && widget.dimensions[1] === newRows) {
    return [...widgets];
  }
  if (newCols < 1 || newRows < 1 || newCols > gridCols || newRows > gridRows) {
    return [...widgets];
  }

  // 1. Try current origin
  if (canPlaceWidget(widgets, { ...widget, dimensions: newDimensions }, gridCols, gridRows)) {
    return widgets.map((w) =>
      w.widget_id === widgetId ? { ...w, dimensions: newDimensions } : w,
    );
  }

  // 2. Try clamped origin
  const clampedCol = Math.min(widget.origin[0], gridCols - newCols);
  const clampedRow = Math.min(widget.origin[1], gridRows - newRows);
  if (clampedCol >= 0 && clampedRow >= 0) {
    if (
      canPlaceWidget(
        widgets,
        { ...widget, origin: [clampedCol, clampedRow], dimensions: newDimensions },
        gridCols,
        gridRows,
      )
    ) {
      return widgets.map((w) =>
        w.widget_id === widgetId
          ? { ...w, origin: [clampedCol, clampedRow], dimensions: newDimensions }
          : w,
      );
    }
  }

  // 3. Scan for first open slot on widget's screen
  for (let r = 0; r <= gridRows - newRows; r++) {
    for (let c = 0; c <= gridCols - newCols; c++) {
      if (
        canPlaceWidget(
          widgets,
          { ...widget, origin: [c, r], dimensions: newDimensions },
          gridCols,
          gridRows,
        )
      ) {
        return widgets.map((w) =>
          w.widget_id === widgetId ? { ...w, origin: [c, r], dimensions: newDimensions } : w,
        );
      }
    }
  }

  return [...widgets];
}

/**
 * Toggles pinned state of a widget across all screens if collision-free.
 */
export function togglePinWidget(
  widgets: readonly LayoutEditorWidget[],
  widgetId: string,
  gridCols: number,
  gridRows: number,
  activeScreen = 0,
): LayoutEditorWidget[] {
  const widget = widgets.find((w) => w.widget_id === widgetId);
  if (!widget) return [...widgets];

  if (widget.pinned) {
    const targetScreen = Math.max(0, activeScreen);
    const updated = widgets.map((w) =>
      w.widget_id === widgetId ? { ...w, pinned: false, screen: targetScreen } : w,
    );
    return compactScreens(updated);
  }

  const maxS = getTotalScreens(widgets);
  const targetMask = computeWidgetBitmask(widget.origin, widget.dimensions, gridCols);
  for (let s = 0; s < maxS; s++) {
    const sMask = computeScreenMask(widgets, s, gridCols, gridRows, widgetId);
    if ((sMask & targetMask) !== 0) {
      return [...widgets];
    }
  }

  const updated = widgets.map((w) =>
    w.widget_id === widgetId ? { ...w, pinned: true, screen: 0 } : w,
  );
  return compactScreens(updated);
}
