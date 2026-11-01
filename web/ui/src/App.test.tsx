import { act, cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

describe('App', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('renders the display shell with the carousel grid canvas', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ current_screen: 0, interval_seconds: 30, screens: [{ index: 0, widgets: [] }] }), { status: 200 })),
    );
    const { container } = render(<App />);
    await act(async () => {});

    const shell = container.querySelector('#mirrormere-app');
    expect(shell).not.toBeNull();
    expect(shell?.querySelectorAll('#grid-canvas')).toHaveLength(1);
    expect(shell?.querySelector('.carousel-track')).not.toBeNull();
  });
});
