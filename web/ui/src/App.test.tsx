import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { App } from './App';

describe('App', () => {
  it('renders the empty display shell with a grid canvas', () => {
    const { container } = render(<App />);
    const shell = container.querySelector('#mirrormere-app');
    expect(shell).not.toBeNull();
    expect(shell?.querySelector('#grid-canvas')).not.toBeNull();
    expect(shell?.children).toHaveLength(1);
  });
});
