# chat-log

A rolling conversation transcript, newest message at the bottom, for an ambient
(touch or e-ink) display. It works out of the box: no database, no endpoint and
no external pusher. The voice hub records every voice turn into an in-memory
store and the widget renders from it.

## How it gets its data

- **Voice turns (automatic).** On every `POST /api/voice/interact` the hub
  records the transcribed user text (`role: human`, `author` = the identified
  speaker when speaker ID is on, otherwise empty) and the brain's final reply
  (`role: agent`), each with a timestamp, under the voice node that sent it.
- **Pushes (optional).** Anything else can `POST /api/widgets/{widget_id}/push`
  with the payload below (validated against `response_schema`; `400` on bad
  input). A push replaces that widget's own pushed transcript, which is shown
  together with the node's voice conversation. Same TTL and cap.

Every recorded turn and push goes through the normal widget push path
(cache update plus a `widget.update` broadcast on `GET /api/events`), so every
display re-renders and picks its own conversation. There is no endpoint and no
polling, so nothing blanks the widget between turns. Nothing is written to disk. Messages expire after a TTL (default 20 minutes)
and each node keeps at most 50 messages; a restart starts empty. Tune both in
`config.yaml`:

```yaml
chat_log:
  ttl_minutes: 20
  max_messages_per_node: 50
```

## Which conversation a display shows

One Mirrormere server can drive several devices, so the widget config does not
pick a node. The display identifies itself instead: open the display page with
its voice `node_id` (the same one in that device's `voice.yaml`) as the `node`
query parameter, and the web client forwards it on every widget render
request.

| Device | Display URL |
|---|---|
| Wall kiosk | `deploy/kiosk/session.sh` appends `?node=<id>` itself, taking the id from `MIRRORMERE_NODE_ID` or the ear config (`/etc/mirrormere/voice.yaml`), so it is set once per device |
| E-ink renderer | `DISPLAY_URL=http://mirrormere-core:8080/display?node=eink-display-livingroom` |

With no `node` parameter the widget shows the most recently active node's
conversation.

## Widget config

```yaml
- id: voice-chat
  type: chat-log
  dimensions: [3, 2]
  config:
    title: "Kitchen assistant"   # optional header, default "Chat"
```

## Push payload

```json
{
  "messages": [
    {"role": "human", "author": "sam", "ts": "2026-01-02T18:39:10Z", "text": "What is on the calendar tomorrow?"},
    {"role": "agent", "author": "assistant", "ts": "2026-01-02T18:39:18Z", "text": "Dentist at 9:00."}
  ]
}
```

| Field | Required | Notes |
|---|---|---|
| `messages` | no | Oldest first. Empty clears the pushed transcript. |
| `messages[].role` | yes | `human` or `agent`. Humans render bold with a solid left rule; agents render regular with a dotted rule. |
| `messages[].text` | yes | Plain text, HTML-escaped. |
| `messages[].author` | no | Falls back to the role name. |
| `messages[].ts` | no | RFC 3339; shown as `HH:MM`. Defaults to the receive time. Messages older than the TTL are dropped. |

`testdata/sample.json` is the render data shape (`updated_at`, the header time,
is the newest message's time; the header title comes from `config.title`).

## Styling

The template is unconditional and uses `var(--mm-*)` tokens plus semantic
classes (`chat-log-header`, `chat-log-message`, `chat-log-human`,
`chat-log-agent`, `chat-log-meta`, `chat-log-author`, `chat-log-ts`,
`chat-log-text`, ...). Per SPEC-014 the e-ink presentation lives in
`deploy/examples/eink.css`, not in the widget. The widget does not truncate;
the container clips the oldest messages that do not fit.
