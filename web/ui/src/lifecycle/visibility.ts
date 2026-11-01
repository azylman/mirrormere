import { useDisplayStore } from '../store';

/**
 * Single source for document visibility. Every timer and rAF loop in the React
 * UI subscribes to useDocumentVisible() and stops while hidden. The event stream
 * stays connected either way.
 */
export function observeDocumentVisibility(
  doc: Document,
  win: Window,
  setVisible: (visible: boolean) => void,
): () => void {
  const onVisibilityChange = () => setVisible(!doc.hidden);
  const onPageHide = () => setVisible(false);
  const onPageShow = () => setVisible(!doc.hidden);

  doc.addEventListener('visibilitychange', onVisibilityChange);
  win.addEventListener('pagehide', onPageHide);
  win.addEventListener('pageshow', onPageShow);
  setVisible(!doc.hidden);

  return () => {
    doc.removeEventListener('visibilitychange', onVisibilityChange);
    win.removeEventListener('pagehide', onPageHide);
    win.removeEventListener('pageshow', onPageShow);
  };
}

export function useDocumentVisible(): boolean {
  return useDisplayStore((s) => s.visibility.visible);
}
