import { memo } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import { formatTime } from '../format';

export interface ChatTurn {
  query: string;
  response?: string;
  timestamp?: string;
}

/**
 * Normalizes raw chat messages into turn pairs (user query + optional assistant response).
 * Supports both turn objects ({ query, response, timestamp }) and sequential role messages
 * ({ role: 'human' | 'agent', text, ts }).
 */
export function normalizeChatTurns(messages: unknown[]): ChatTurn[] {
  const turns: ChatTurn[] = [];

  for (let i = 0; i < messages.length; i++) {
    const item = messages[i];
    if (!item || typeof item !== 'object') continue;
    const m = item as Record<string, unknown>;

    // Case 1: Pre-formed turn object
    const query = m.query ?? m.Query;
    if (query !== undefined) {
      const response = m.response ?? m.Response;
      const rawTs = m.timestamp ?? m.Timestamp ?? m.ts ?? m.time;
      const tsStr = typeof rawTs === 'string' ? rawTs.trim() : '';
      const formatted = tsStr !== '' ? (formatTime('15:04', tsStr) || tsStr) : undefined;
      turns.push({
        query: String(query),
        response: response !== undefined && response !== null && String(response).trim() !== ''
          ? String(response)
          : undefined,
        timestamp: formatted,
      });
      continue;
    }

    // Case 2: Role-based message sequence (human / agent)
    const role = String(m.role ?? m.Role ?? '').toLowerCase();
    const text = String(m.text ?? m.Text ?? '');
    const rawTs = m.ts ?? m.timestamp ?? m.Timestamp ?? m.time;
    const tsStr = typeof rawTs === 'string' ? rawTs.trim() : '';
    const formatted = tsStr !== '' ? (formatTime('15:04', tsStr) || tsStr) : undefined;

    if (role === 'human' || role === 'user') {
      let response: string | undefined = undefined;
      if (i + 1 < messages.length) {
        const next = messages[i + 1];
        if (next && typeof next === 'object') {
          const nextM = next as Record<string, unknown>;
          const nextRole = String(nextM.role ?? nextM.Role ?? '').toLowerCase();
          if (nextRole === 'agent' || nextRole === 'assistant') {
            response = String(nextM.text ?? nextM.Text ?? '');
            i++; // Consume assistant turn
          }
        }
      }
      turns.push({
        query: text,
        response: response !== undefined && response.trim() !== '' ? response : undefined,
        timestamp: formatted,
      });
    } else if (role === 'agent' || role === 'assistant') {
      turns.push({
        query: '',
        response: text.trim() !== '' ? text : undefined,
        timestamp: formatted,
      });
    } else {
      turns.push({
        query: text,
        response: undefined,
        timestamp: formatted,
      });
    }
  }

  return turns;
}

/**
 * Native React widget component for chat-log voice history and conversation transcripts.
 * Ports widgets/chat-log/views/widget.html.
 */
export const ChatLogWidget = memo(function ChatLogWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const config = entry?.meta?.config as Record<string, unknown> | undefined;
  const data = entry?.data as Record<string, unknown> | undefined;
  const title = (typeof config?.title === 'string' && config.title.trim() !== '' && config.title) || 'Voice History';
  const isError = entry?.state === 'error';

  const rawMessages = Array.isArray(data?.messages)
    ? data.messages
    : Array.isArray(data?.Messages)
      ? data.Messages
      : [];

  const turns = normalizeChatTurns(rawMessages);

  return (
    <div className="widget-chat-log">
      {isError && (
        <div className="widget-error-banner">
          <span className="error-icon">⚠</span>
          <span>Widget Error</span>
        </div>
      )}

      {rawMessages.length > 0 ? (
        <>
          <div className="chat-log-header">
            <span className="chat-log-title">{title}</span>
          </div>
          <div className="chat-log-messages">
            {turns.map((turn, i) => {
              const hasQuery = Boolean(turn.query && turn.query.trim() !== '');
              const hasResponse = Boolean(turn.response && turn.response.trim() !== '');
              if (!hasQuery && !hasResponse) return null;

              // When an agent turn is standalone (e.g. earlier human message pruned or pushed directly),
              // omit the empty 'You' bubble and display the timestamp on the assistant bubble instead.
              const showUserBubble = hasQuery || !hasResponse;
              const showAssistantTimestamp = !showUserBubble && Boolean(turn.timestamp);

              return (
                <div key={i} className="chat-log-turn">
                  {showUserBubble && (
                    <div className="chat-log-bubble chat-log-user">
                      <div className="chat-log-meta">
                        <span className="chat-log-speaker">You</span>
                        {turn.timestamp ? (
                          <span className="chat-log-time"> {turn.timestamp}</span>
                        ) : null}
                      </div>
                      <div className="chat-log-text">{turn.query}</div>
                    </div>
                  )}
                  {hasResponse && (
                    <div className="chat-log-bubble chat-log-assistant">
                      <div className="chat-log-meta">
                        <span className="chat-log-speaker">Assistant</span>
                        {showAssistantTimestamp ? (
                          <span className="chat-log-time"> {turn.timestamp}</span>
                        ) : null}
                      </div>
                      <div className="chat-log-text">{turn.response}</div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </>
      ) : (
        <div className="chat-log-empty">
          <span>No messages yet</span>
        </div>
      )}
    </div>
  );
});
