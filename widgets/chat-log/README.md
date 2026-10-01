# chat-log

A rolling conversation transcript, newest message at the bottom. Meant for
voice-assistant or chat-bot output on an ambient (touch or e-ink) display. The
widget has no data source of its own: an external process pushes the payload
with `POST /api/widgets/{widget_id}/push`.

## Payload

```json
{
  "session": "Kitchen assistant",
  "updated_at": "2026-01-02T18:42:00Z",
  "messages": [
    {"role": "human", "author": "sam", "ts": "2026-01-02T18:39:10Z", "text": "What is on the calendar tomorrow?"},
    {"role": "agent", "author": "assistant", "ts": "2026-01-02T18:39:18Z", "text": "Dentist at 9:00."}
  ]
}
```

| Field | Required | Notes |
|---|---|---|
| `session` | no | Header title; falls back to "Chat". |
| `updated_at` | no | Header timestamp, shown as `HH:MM`. Use the newest message's time, not the push time, so an unchanged transcript re-renders identically (useful on e-ink, where an identical image means no panel write). |
| `messages` | no | Oldest first. Empty or absent renders "No messages yet". |
| `messages[].role` | yes | `human` or `agent`. Humans render bold with a solid left rule; agents render regular with a dotted rule. |
| `messages[].text` | yes | Plain text, HTML-escaped. |
| `messages[].author` | no | Falls back to the role name. |
| `messages[].ts` | no | Shown as `HH:MM` next to the author. |

The widget does not truncate; the container clips the oldest messages that do
not fit. Push as many as you like and keep the newest at the end.
`testdata/sample.json` is a complete example.

## Styling

The template is unconditional and uses `var(--mm-*)` tokens plus semantic
classes (`chat-log-header`, `chat-log-message`, `chat-log-human`,
`chat-log-agent`, `chat-log-meta`, `chat-log-author`, `chat-log-ts`,
`chat-log-text`, ...). Per SPEC-014 the e-ink presentation lives in
`deploy/examples/eink.css`, not in the widget.

## Deployment

Declare an instance in `display.widgets` (see `deploy/examples/config.yaml`).
Because the `http` provider currently requires an endpoint, a push-only
instance points `endpoint` at a harmless 2xx JSON URL such as the core's own
`/healthz`, with a long `refresh_interval_seconds`. Each poll replaces the
cached data, so the next push repopulates the widget.
