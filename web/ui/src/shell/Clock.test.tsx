import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../store';
import { Clock, createClockFormatters, isValidTimezone } from './Clock';

describe('isValidTimezone', () => {
  it('identifies valid IANA timezones', () => {
    expect(isValidTimezone('UTC')).toBe(true);
    expect(isValidTimezone('America/Los_Angeles')).toBe(true);
    expect(isValidTimezone('Europe/London')).toBe(true);
  });

  it('rejects invalid or empty timezone values', () => {
    expect(isValidTimezone('Mars/Olympus_Mons')).toBe(false);
    expect(isValidTimezone('')).toBe(false);
    expect(isValidTimezone(null)).toBe(false);
    expect(isValidTimezone(undefined)).toBe(false);
    expect(isValidTimezone('invalid')).toBe(false);
  });
});

describe('createClockFormatters', () => {
  it('creates formatters once per timezone and memoizes them', () => {
    const tz = 'America/New_York';
    const first = createClockFormatters(tz);
    const second = createClockFormatters(tz);
    expect(first).toBe(second);

    const defaultFirst = createClockFormatters(undefined);
    const defaultSecond = createClockFormatters(undefined);
    expect(defaultFirst).toBe(defaultSecond);
  });

  it('formats time and date with expected parts', () => {
    const date = new Date(2026, 9, 31, 14, 5); // 2:05 PM
    const formatters = createClockFormatters('UTC');
    expect(formatters.formatTime(date)).toBeTruthy();
    expect(formatters.formatDate(date)).toBeTruthy();
  });
});

describe('Clock component', () => {
  let appEl: HTMLDivElement;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-31T14:05:00Z'));
    useDisplayStore.setState({
      header: { timestamp: null, timezone: 'UTC', weather: null },
      visibility: { visible: true },
    });
    appEl = document.createElement('div');
    appEl.id = 'mirrormere-app';
    document.body.appendChild(appEl);
  });

  afterEach(() => {
    vi.useRealTimers();
    if (appEl.parentNode) {
      appEl.parentNode.removeChild(appEl);
    }
    delete document.body.dataset.timezone;
  });

  it('renders exact DOM hierarchy and syncs data-timezone to body, app, and clock', () => {
    const { container } = render(<Clock />);
    const headerBlock = container.querySelector('.header-time-block');
    expect(headerBlock).not.toBeNull();

    const clockEl = container.querySelector('#header-clock') as HTMLDivElement;
    expect(clockEl).not.toBeNull();
    expect(clockEl.dataset.timezone).toBe('UTC');

    const dateStack = container.querySelector('#header-date-stack');
    expect(dateStack).not.toBeNull();

    const dateEl = container.querySelector('#header-date');
    expect(dateEl).not.toBeNull();

    expect(document.body.dataset.timezone).toBe('UTC');
    expect(appEl.dataset.timezone).toBe('UTC');
  });

  it('writes textContent on clockEl.firstChild only when formatted time changes', () => {
    const { container } = render(<Clock />);
    const clockEl = container.querySelector('#header-clock') as HTMLDivElement;
    const textNode = clockEl.firstChild;
    expect(textNode).not.toBeNull();

    const initialText = textNode!.textContent;
    expect(initialText).not.toBe('--:--');

    const setterSpy = vi.spyOn(textNode!, 'textContent', 'set');

    // Advance 10 seconds: same minute, time string unchanged -> no textContent writes
    act(() => {
      vi.advanceTimersByTime(10000);
    });
    expect(setterSpy).not.toHaveBeenCalled();

    // Advance past minute boundary: time string changes -> exactly one textContent write
    act(() => {
      vi.advanceTimersByTime(51000);
    });
    expect(setterSpy).toHaveBeenCalledTimes(1);

    setterSpy.mockRestore();
  });

  it('stops its timer when document is hidden and resumes when visible', () => {
    const setTimeoutSpy = vi.spyOn(globalThis, 'setTimeout');
    render(<Clock />);

    const callCountBeforeHide = setTimeoutSpy.mock.calls.length;

    // Document becomes hidden
    act(() => {
      useDisplayStore.getState().setDocumentVisible(false);
    });

    const callCountAfterHide = setTimeoutSpy.mock.calls.length;

    // Advance time while hidden: no new setTimeout calls should be scheduled
    act(() => {
      vi.advanceTimersByTime(10000);
    });
    expect(setTimeoutSpy.mock.calls.length).toBe(callCountAfterHide);

    // Document becomes visible again: timer resumes
    act(() => {
      useDisplayStore.getState().setDocumentVisible(true);
    });
    expect(setTimeoutSpy.mock.calls.length).toBeGreaterThan(callCountBeforeHide);

    setTimeoutSpy.mockRestore();
  });

  it('prefers document.body.dataset.timezone before header.update event arrives', () => {
    document.body.dataset.timezone = 'America/Los_Angeles';
    const { container } = render(<Clock />);

    const clockEl = container.querySelector('#header-clock') as HTMLDivElement;
    expect(clockEl.dataset.timezone).toBe('America/Los_Angeles');
    expect(document.body.dataset.timezone).toBe('America/Los_Angeles');

    // Once header.update arrives with a timestamp, header.timezone takes precedence
    act(() => {
      useDisplayStore.setState({
        header: { timestamp: '2026-10-31T14:05:00Z', timezone: 'America/New_York', weather: null },
      });
    });

    expect(clockEl.dataset.timezone).toBe('America/New_York');
    expect(document.body.dataset.timezone).toBe('America/New_York');
  });
});
