import { useEffect } from 'react';
import { useHeader } from '../store/hooks';
import { Clock, resolveActiveTimezone } from './Clock';
import { HeaderVideoButton } from './HeaderVideoButton';
import { NextUp } from './NextUp';
import { StatusDot } from './StatusDot';
import { WeatherPill } from './WeatherPill';
import { applyCursorSuppression } from './cursor';
import { ThemeToggle } from './theme';

export interface HeaderProps {
  timezone?: string;
}

export function Header({ timezone: propTimezone }: HeaderProps = {}) {
  const header = useHeader();
  const timezone = resolveActiveTimezone(propTimezone, header);

  useEffect(() => {
    applyCursorSuppression();
  }, []);

  useEffect(() => {
    if (typeof document !== 'undefined' && timezone) {
      if (document.body?.dataset) {
        document.body.dataset.timezone = timezone;
      }
      const appEl = document.getElementById('mirrormere-app');
      if (appEl?.dataset) {
        appEl.dataset.timezone = timezone;
      }
    }
  }, [timezone]);

  return (
    <header className="header-zone">
      <Clock timezone={propTimezone} />
      <NextUp timezone={propTimezone} />
      <div className="header-right-cluster">
        <HeaderVideoButton />
        <WeatherPill />
        <ThemeToggle />
        <StatusDot />
      </div>
      <div className="header-voice" id="header-voice">
        <div className="voice-indicator" id="voice-indicator">
          <span className="voice-pulse-ring" id="voice-pulse-ring" />
          <span className="voice-dot" id="voice-dot" style={{ display: 'none' }} />
          <span className="voice-icon" id="voice-icon">
            <svg
              width="22"
              height="22"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              className="mm-voice-icon-svg"
            >
              <path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z" />
              <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
              <line x1="12" y1="19" x2="12" y2="22" />
            </svg>
          </span>
          <span className="voice-label" id="voice-label" />
        </div>
        <span className="voice-status" id="voice-status" style={{ display: 'none' }} />
        <div className="voice-transcript" id="voice-transcript" style={{ display: 'none' }} />
        <button
          id="header-voice-dismiss-btn"
          className="header-voice-dismiss"
          type="button"
          aria-label="Dismiss Voice"
          title="Dismiss Voice"
          onClick={() => {
            if (typeof window !== 'undefined') {
              const win = window as unknown as { MirrormereVoice?: { resetHUD?: () => void } };
              win.MirrormereVoice?.resetHUD?.();
            }
          }}
        >
          ✕
        </button>
        <div className="voice-audio-progress" id="voice-audio-progress" style={{ display: 'none' }} />
      </div>
    </header>
  );
}
