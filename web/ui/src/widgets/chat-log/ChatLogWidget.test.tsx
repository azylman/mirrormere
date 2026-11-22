import { act } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import sampleData from '../../../../../widgets/chat-log/testdata/sample.json';
import { useDisplayStore } from '../../store';
import { renderWidget, seedWidget, widgetMeta } from '../../test-utils/widget';
import { ChatLogWidget } from './ChatLogWidget';
import { itemAt } from '../../test-utils/lookup';

function renderChatLog(data: unknown, { config = {}, state = 'healthy' }: { config?: Record<string, unknown> | undefined; state?: string | undefined } = {}) {
  seedWidget('chat-1', widgetMeta('chat-log', config, [3, 2]), data, state);
  return renderWidget(ChatLogWidget, 'chat-1', 'chat-log').container;
}

const xssText = '<script>window.__pwned = true;</script><b>Hello</b><img src="x" onerror="alert(1)"/>';

describe('ChatLogWidget', () => {
  // wantText: [selector, exact textContent]; wantAbsent: selectors that must not match.
  const tests: {
    name: string;
    data: unknown;
    config?: Record<string, unknown>;
    state?: string;
    wantText: [string, string][];
    wantAbsent: string[];
  }[] = [
    {
      name: 'renders empty state on unhydrated data {}',
      data: {},
      wantText: [['.chat-log-empty', 'No messages yet']],
      wantAbsent: ['.chat-log-messages', '.chat-log-header'],
    },
    {
      name: 'renders empty state on empty messages []',
      data: { messages: [] },
      wantText: [['.chat-log-empty', 'No messages yet']],
      wantAbsent: ['.chat-log-messages'],
    },
    {
      name: 'falls back session to config.title without an updated stamp',
      data: { messages: [{ role: 'human', text: 'Hello' }] },
      config: { title: 'Living Room Chat' },
      wantText: [['.chat-log-session', 'Living Room Chat']],
      wantAbsent: ['.chat-log-updated'],
    },
    {
      name: 'falls back session to Chat',
      data: { messages: [{ role: 'agent', text: 'Ready' }] },
      wantText: [['.chat-log-session', 'Chat']],
      wantAbsent: [],
    },
    {
      name: 'falls back author to role when author is omitted',
      data: { messages: [{ role: 'human', text: 'Test query' }, { role: 'agent', text: 'Test response' }] },
      wantText: [['.chat-log-human .chat-log-author', 'human'], ['.chat-log-agent .chat-log-author', 'agent']],
      wantAbsent: [],
    },
    {
      name: 'renders widget-error-banner when state is error',
      data: {},
      state: 'error',
      wantText: [['.widget-error-banner', '⚠Widget Error']],
      wantAbsent: [],
    },
    {
      name: 'escapes HTML and script tags in text safely',
      data: { messages: [{ role: 'human', author: '<evil>', text: xssText }] },
      wantText: [['.chat-log-text', xssText], ['.chat-log-author', '<evil>']],
      wantAbsent: ['script', 'img'],
    },
  ];

  for (const tc of tests) {
    it(tc.name, () => {
      const container = renderChatLog(tc.data, { config: tc.config, state: tc.state });
      for (const [selector, text] of tc.wantText) {
        expect(container.querySelector(selector)?.textContent, selector).toBe(text);
      }
      for (const selector of tc.wantAbsent) {
        expect(container.querySelector(selector), selector).toBeNull();
      }
    });
  }

  it('re-renders in place as messages arrive and falls back to the empty state when the entry is removed', () => {
    const container = renderChatLog({});
    expect(container.querySelector('.chat-log-empty')?.textContent).toBe('No messages yet');

    act(() => {
      seedWidget('chat-1', widgetMeta('chat-log', {}, [3, 2]), { messages: [{ role: 'agent', text: 'Ready' }] }, 'healthy');
    });
    expect(container.querySelector('.chat-log-session')?.textContent).toBe('Chat');
    expect(container.querySelector('.chat-log-text')?.textContent).toBe('Ready');

    act(() => {
      useDisplayStore.setState({ widgets: {} });
    });
    expect(container.querySelector('.chat-log-empty')?.textContent).toBe('No messages yet');
    expect(container.querySelector('.chat-log-header')).toBeNull();
  });

  it('renders messages from the sample data', () => {
    const container = renderChatLog(sampleData);
    const root = container.querySelector('.widget-chat-log') as HTMLElement;
    expect(root).not.toBeNull();
    expect(root.querySelector('.chat-log-session')?.textContent).toBe('Kitchen assistant');
    expect(root.querySelector('.chat-log-updated')?.textContent).toBe('18:42');
    expect(root.querySelector('.chat-log-messages')).not.toBeNull();

    const want = [
      { cls: 'chat-log-human', author: 'sam', ts: ' 18:39', text: 'What is on the calendar tomorrow?' },
      { cls: 'chat-log-agent', author: 'assistant', ts: ' 18:39', text: 'Dentist at 9:00 and soccer practice at 4:30.' },
      { cls: 'chat-log-human', author: 'sam', ts: ' 18:41', text: 'Add milk and eggs to the shopping list.' },
      { cls: 'chat-log-agent', author: 'assistant', ts: ' 18:42', text: 'Done. Milk and eggs are on the shopping list.' },
    ];
    const messages = root.querySelectorAll('.chat-log-message');
    expect(messages.length).toBe(want.length);
    want.forEach((w, i) => {
      const msg = itemAt(messages, i);
      expect(msg.classList.contains(w.cls)).toBe(true);
      expect(msg.querySelector('.chat-log-author')?.textContent).toBe(w.author);
      expect(msg.querySelector('.chat-log-ts')?.textContent).toBe(w.ts);
      expect(msg.querySelector('.chat-log-text')?.textContent).toBe(w.text);
    });
  });

  it('handles non-object messages, missing authors/roles, and PascalCase fields', () => {
    const container = renderChatLog({
      UpdatedAt: '2026-10-31T14:30:00Z',
      Messages: [
        null,
        'invalid-primitive',
        { Text: 'No author or role', time: '' },
        { role: 'human', author: '   ', text: 'Whitespace author' },
      ],
    });
    const messages = container.querySelectorAll('.chat-log-message');
    expect(messages.length).toBe(2);

    // First valid message: role defaults to agent, no ts
    expect(itemAt(messages, 0).classList.contains('chat-log-agent')).toBe(true);
    expect(itemAt(messages, 0).querySelector('.chat-log-author')?.textContent).toBe('agent');
    expect(itemAt(messages, 0).querySelector('.chat-log-ts')).toBeNull();

    // Second valid message: author is whitespace so falls back to human
    expect(itemAt(messages, 1).classList.contains('chat-log-human')).toBe(true);
    expect(itemAt(messages, 1).querySelector('.chat-log-author')?.textContent).toBe('human');
  });

  it('handles config.Title, data.updatedAt with raw fallback, and non-date message timestamps', () => {
    const container = renderChatLog(
      {
        updatedAt: 'raw-updated-string',
        messages: [
          { role: 'system', Timestamp: 'raw-timestamp-string', text: 'System alert message' },
          { role: 'assistant', timestamp: '2026-10-31T15:00:00Z', text: 'Assistant response' },
        ],
      },
      { config: { Title: 'Kitchen Channel' } },
    );
    expect(container.querySelector('.chat-log-session')?.textContent).toBe('Kitchen Channel');
    expect(container.querySelector('.chat-log-updated')?.textContent).toBe('raw-updated-string');

    const messages = container.querySelectorAll('.chat-log-message');
    expect(messages.length).toBe(2);
    expect(itemAt(messages, 0).querySelector('.chat-log-ts')?.textContent).toContain('raw-timestamp-string');
    expect(itemAt(messages, 0).querySelector('.chat-log-author')?.textContent).toBe('agent');
    expect(itemAt(messages, 1).querySelector('.chat-log-ts')?.textContent).toContain('15:00');
  });
});
