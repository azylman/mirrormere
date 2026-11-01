import { useCallback, useEffect, useRef } from 'react';
import { useDisplayStore } from '../../store';

export interface UseInactivityResetProps {
  isInteractive: boolean;
  onReset: () => void;
}

export function useInactivityReset({
  isInteractive,
  onReset,
}: UseInactivityResetProps) {
  const hasPausedStoreRef = useRef(false);
  const inactivityTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onResetRef = useRef(onReset);
  onResetRef.current = onReset;

  const clearInactivityTimer = useCallback(() => {
    if (inactivityTimerRef.current) {
      clearTimeout(inactivityTimerRef.current);
      inactivityTimerRef.current = null;
    }
  }, []);

  const resetInactivityTimer = useCallback(() => {
    clearInactivityTimer();
    inactivityTimerRef.current = setTimeout(() => {
      onResetRef.current();
      if (hasPausedStoreRef.current) {
        useDisplayStore.getState().resume();
        hasPausedStoreRef.current = false;
      }
    }, 60000);
  }, [clearInactivityTimer]);

  const handleUserActivity = useCallback(() => {
    if (hasPausedStoreRef.current || inactivityTimerRef.current !== null) {
      resetInactivityTimer();
    }
  }, [resetInactivityTimer]);

  useEffect(() => {
    if (isInteractive) {
      if (!hasPausedStoreRef.current) {
        useDisplayStore.getState().pause();
        hasPausedStoreRef.current = true;
      }
      resetInactivityTimer();
    } else {
      clearInactivityTimer();
      if (hasPausedStoreRef.current) {
        useDisplayStore.getState().resume();
        hasPausedStoreRef.current = false;
      }
    }
  }, [isInteractive, resetInactivityTimer, clearInactivityTimer]);

  useEffect(() => {
    return () => {
      clearInactivityTimer();
      if (hasPausedStoreRef.current) {
        useDisplayStore.getState().resume();
        hasPausedStoreRef.current = false;
      }
    };
  }, [clearInactivityTimer]);

  return {
    handleUserActivity,
    clearInactivityTimer,
    resetInactivityTimer,
  };
}
