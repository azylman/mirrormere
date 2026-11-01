/**
 * Cursor suppression query overrides (?hide_cursor=true/false, ?cursor=none/visible).
 * Ported from web/static/js/display.js lines 860-871.
 */
export function applyCursorSuppression(search?: string, body?: HTMLElement | null): void {
  const targetBody = body !== undefined ? body : (typeof document !== 'undefined' ? document.body : null);
  if (!targetBody) return;

  const searchStr = search !== undefined ? search : (typeof window !== 'undefined' && window.location ? window.location.search : '');
  if (!searchStr) return;

  const urlParams = new URLSearchParams(searchStr);
  const hideCursor = urlParams.get('hide_cursor');
  const cursor = urlParams.get('cursor');

  if (hideCursor === 'true' || cursor === 'none') {
    targetBody.classList.add('mm-touch-display', 'mm-touch-kiosk');
  } else if (hideCursor === 'false' || cursor === 'visible') {
    targetBody.classList.remove('mm-touch-display', 'mm-touch-kiosk');
  }
}
