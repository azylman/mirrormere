import { cleanup, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import sampleData from '../../../../../widgets/chat-log/testdata/sample.json';
import { useDisplayStore } from '../../store';
import { ChatLogWidget } from './ChatLogWidget';

const initialStore = useDisplayStore.getState();

describe('ChatLogWidget', () => {
  beforeEach(() => {
    useDisplayStore.setState(initialStore, true);
  });

  afterEach(() => {
    cleanup();
  });

  it('renders transcript turns with sample.json data', () => {
    useDisplayStore.setState({
      widgets: {
        'chat-1': {
          meta: {
            type: 'chat-log',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: sampleData,
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(<ChatLogWidget id="chat-1" type="chat-log" />);
    const root = container.querySelector('.widget-chat-log') as HTMLElement;
    expect(root).not.toBeNull();

    // Default title
    const title = root.querySelector('.chat-log-title');
    expect(title?.textContent).toBe('Voice History');

    // Messages container
    const messages = root.querySelector('.chat-log-messages');
    expect(messages).not.toBeNull();

    const turns = root.querySelectorAll('.chat-log-turn');
    expect(turns.length).toBe(2);

    // Turn 1
    const turn1User = turns[0].querySelector('.chat-log-bubble.chat-log-user');
    expect(turn1User).not.toBeNull();
    expect(turn1User?.textContent).toContain('You');
    expect(turn1User?.textContent).toContain('What is on the calendar tomorrow?');
    expect(turn1User?.querySelector('.chat-log-time')?.textContent).toBe(' 18:39');

    const turn1Assistant = turns[0].querySelector('.chat-log-bubble.chat-log-assistant');
    expect(turn1Assistant).not.toBeNull();
    expect(turn1Assistant?.textContent).toContain('Assistant');
    expect(turn1Assistant?.textContent).toContain('Dentist at 9:00 and soccer practice at 4:30.');

    // Turn 2
    const turn2User = turns[1].querySelector('.chat-log-bubble.chat-log-user');
    expect(turn2User?.textContent).toContain('Add milk and eggs to the shopping list.');

    const turn2Assistant = turns[1].querySelector('.chat-log-bubble.chat-log-assistant');
    expect(turn2Assistant?.textContent).toContain('Done. Milk and eggs are on the shopping list.');
  });

  it('renders empty state on unhydrated data: {} and empty messages: []', () => {
    // Unhydrated data: {}
    useDisplayStore.setState({
      widgets: {
        'chat-unhydrated': {
          state: 'healthy',
          data: {},
          timestamp: '',
        },
      },
    });

    const { container: c1 } = render(<ChatLogWidget id="chat-unhydrated" type="chat-log" />);
    const empty1 = c1.querySelector('.chat-log-empty');
    expect(empty1).not.toBeNull();
    expect(empty1?.textContent).toBe('No messages yet');
    expect(c1.querySelector('.chat-log-messages')).toBeNull();

    // Empty array messages: []
    useDisplayStore.setState({
      widgets: {
        'chat-empty': {
          state: 'healthy',
          data: { messages: [] },
          timestamp: '',
        },
      },
    });

    const { container: c2 } = render(<ChatLogWidget id="chat-empty" type="chat-log" />);
    const empty2 = c2.querySelector('.chat-log-empty');
    expect(empty2).not.toBeNull();
    expect(empty2?.textContent).toBe('No messages yet');
    expect(c2.querySelector('.chat-log-messages')).toBeNull();
  });

  it('renders custom config.title when specified', () => {
    useDisplayStore.setState({
      widgets: {
        'chat-title': {
          meta: {
            type: 'chat-log',
            origin: [0, 0],
            dimensions: [3, 2],
            config: { title: 'Kitchen Assistant' },
            assets_base: '',
          },
          state: 'healthy',
          data: {
            messages: [{ query: 'Turn on kitchen lights', response: 'Lights turned on' }],
          },
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(<ChatLogWidget id="chat-title" type="chat-log" />);
    const title = container.querySelector('.chat-log-title');
    expect(title?.textContent).toBe('Kitchen Assistant');
  });

  it('renders widget-error-banner when state is error', () => {
    useDisplayStore.setState({
      widgets: {
        'chat-error': {
          state: 'error',
          data: {},
          timestamp: '',
        },
      },
    });

    const { container } = render(<ChatLogWidget id="chat-error" type="chat-log" />);
    const banner = container.querySelector('.widget-error-banner');
    expect(banner).not.toBeNull();
    expect(banner?.textContent).toContain('⚠');
    expect(banner?.textContent).toContain('Widget Error');
  });

  it('escapes HTML and script tags in query and response safely', () => {
    const maliciousQuery = '<script>window.__pwned = true;</script><b>Hello</b>';
    const maliciousResponse = '<img src="x" onerror="alert(1)"/><i>World</i>';

    useDisplayStore.setState({
      widgets: {
        'chat-xss': {
          state: 'healthy',
          data: {
            messages: [
              {
                query: maliciousQuery,
                response: maliciousResponse,
              },
            ],
          },
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(<ChatLogWidget id="chat-xss" type="chat-log" />);
    // Script or img elements must NOT exist in the DOM
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('img')).toBeNull();

    // Text content must contain the exact raw string safely rendered
    const userText = container.querySelector('.chat-log-user .chat-log-text');
    expect(userText?.textContent).toBe(maliciousQuery);

    const assistantText = container.querySelector('.chat-log-assistant .chat-log-text');
    expect(assistantText?.textContent).toBe(maliciousResponse);
  });

  it('omits user bubble and places timestamp on assistant bubble for standalone agent messages', () => {
    useDisplayStore.setState({
      widgets: {
        'chat-standalone-agent': {
          meta: {
            type: 'chat-log',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {
            messages: [
              {
                role: 'agent',
                text: 'Reminder: Dinner reservation at 7:00 PM.',
                ts: '2026-10-31T19:00:00Z',
              },
            ],
          },
          timestamp: '2026-10-31T19:00:00Z',
        },
      },
    });

    const { container } = render(<ChatLogWidget id="chat-standalone-agent" type="chat-log" />);
    // No empty 'You' bubble should be rendered
    expect(container.querySelector('.chat-log-user')).toBeNull();

    // Assistant bubble should be rendered with text and timestamp
    const assistantBubble = container.querySelector('.chat-log-assistant');
    expect(assistantBubble).not.toBeNull();
    expect(assistantBubble?.textContent).toContain('Assistant');
    expect(assistantBubble?.textContent).toContain('Reminder: Dinner reservation at 7:00 PM.');

    const timeEl = assistantBubble?.querySelector('.chat-log-time');
    expect(timeEl).not.toBeNull();
    expect(timeEl?.textContent).toBe(' 19:00');
  });
});
