/**
 * swipe-point.mjs - picks where a bench swipe is centred so it avoids swipe-blocking widgets.
 *
 * Rects are DOMRect-like objects in CSS pixels: { left, top, right, bottom }. A rect with no
 * area is ignored, since a hidden element reports 0x0 at the origin.
 *
 * pickSwipePoint returns `point` when no blocked rect contains it. Otherwise it returns the
 * centre of the widest widget rect that intersects no blocked rect. If no widget qualifies it
 * returns `point` unchanged, so the caller can still send the swipe.
 */

const hasArea = (r) => r.right - r.left > 0 && r.bottom - r.top > 0;

const contains = (r, x, y) => x >= r.left && x < r.right && y >= r.top && y < r.bottom;

// Edges that only touch do not count as overlap.
const overlaps = (a, b) => a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;

export function pickSwipePoint(point, blockedRects, widgetRects) {
  const blocks = blockedRects.filter(hasArea);
  if (!blocks.some((b) => contains(b, point.x, point.y))) return point;

  let best = null;
  let bestWidth = 0;
  for (const widget of widgetRects) {
    if (!hasArea(widget)) continue;
    const width = widget.right - widget.left;
    if (width <= bestWidth) continue;
    if (blocks.some((b) => overlaps(widget, b))) continue;
    best = widget;
    bestWidth = width;
  }
  if (!best) return point;
  return { x: (best.left + best.right) / 2, y: (best.top + best.bottom) / 2 };
}
