import { createContext, useContext } from 'react';

/**
 * Provided by each Screen pane. Widgets pause timers, decoders and animations while
 * `isCurrentPane` is false, so off-screen neighbours stay mounted but quiet.
 */
export interface PaneContextValue {
  isCurrentPane: boolean;
  paneIndex: number;
}

export const PaneContext = createContext<PaneContextValue>({ isCurrentPane: true, paneIndex: 0 });

export function useIsCurrentPane(): boolean {
  return useContext(PaneContext).isCurrentPane;
}

export function usePaneIndex(): number {
  return useContext(PaneContext).paneIndex;
}
