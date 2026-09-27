# SPEC-014: Family Calendar Widget (`calendar-family`)

## Status
Proposed

**Estimate:** ~2.5 h agent time · ~1.5% of the weekly limit (week and day views, member mapping, 4×2 and 6×2 layouts, e-ink week view). Month view and touch navigation: ~1 h · ~0.6% more.

## Context & Motivation
`calendar-agenda` (SPEC-007 §1) is a chronological list. It answers "what is next", and it is sized to share the screen with other widgets.

A family wall calendar answers a different question: "who is doing what this week". The reference product is the Skylight Calendar. Every event carries its owner's colour, and the default view is the whole family's week at a glance. That puts people at the centre, where the agenda widget puts time.

`calendar-family` is a separate widget package. It reuses the existing calendar provider and adds one concept on top: **members**, the people calendars belong to.

## Goals
- A week view where you can see each person's commitments at a glance, by colour and initial.
- A day view with one column per member, so overlaps between people are visible.
- A layout that works at full screen (6×2) and at two-thirds (4×2).
- An e-ink rendering that stays readable in 1-bit monochrome on the 800×480 panel (SPEC-009).

## Non-Goals
- **Creating or editing events on the display.** SPEC-010 fixes the kiosk as tap-only with no on-screen keyboard. Edits happen phone-first at the source calendar, the same as tasks (SPEC-008). The widget is read-only.
- **Chore charts, meal plans and lists.** Skylight bundles these. Mirrormere already has `tasks` (SPEC-008), and meal planning would be its own widget.
- **Photo frame mode.** That is `photo-carousel`.

---

## Data

### Provider
`provider: calendar-agenda`. No new provider is needed. The widget consumes the same `CalendarSnapshot` payload (SPEC-007 §1, "Normalized Internal Schema"). Member mapping is a presentation concern and is resolved in the view model, not in the provider.

The fetch window is set in `config:`, as `calendar-agenda` does it. The widget passes a window wide enough for its largest view:

| View | Minimum window |
|---|---|
| week | start of the current week through end of week (+7 d) |
| day | today |
| month | first to last day of the displayed month (up to +42 d including padding weeks) |

### Member resolution
Each member claims one or more calendars by `name` (matching `CalendarEvent.calendar_name`). An event belongs to every member whose calendar contains it.

- **Shared events.** An invite that appears on two members' calendars arrives twice. The provider's `id` is derived from the iCal UID plus start time (`evt_<uid>_<unix>`, `internal/provider/calendar.go`), so identical ids are merged into one event owned by several members. It renders once, with every owner's initial.
- **Unclaimed calendars** (holidays, a shared family calendar nobody owns) render in `shared_color` with no initial.
- **Member order** is the order in `members:`. It fixes column order in the day view and initial order on shared events.

### View model
```json
{
  "range": {"start": "2026-09-27", "end": "2026-10-03"},
  "today": "2026-09-27",
  "members": [
    {"name": "Alex", "initial": "A", "color": "#E07A5F", "pattern": "solid"},
    {"name": "Kid",  "initial": "K", "color": "#81B29A", "pattern": "hatch"}
  ],
  "days": [
    {
      "date": "2026-09-27",
      "all_day": [
        {"id": "evt_…", "title": "Trash pickup", "owners": [], "color": "#3D405B"}
      ],
      "timed": [
        {"id": "evt_…", "title": "Soccer", "start": "18:30", "end": "19:30",
         "owners": ["Kid"], "color": "#81B29A", "location": "Field 2"}
      ]
    }
  ]
}
```
`color` is the first owner's colour, or `shared_color` if the event has no owner. On touch displays, an event owned by several members gets a left edge striped in each owner's colour.

---

## Configuration

```yaml
display:
  widgets:
    - id: family-cal
      type: calendar-family
      dimensions: [6, 2]            # or [4, 2]
      refresh_interval_seconds: 300
      config:
        default_view: week          # week | day | month
        days: auto                  # auto | 3 | 5 | 7
        week_starts: sunday         # sunday | monday
        hours: ["07:00", "21:00"]   # visible band in day view; events outside it collapse to an edge marker
        shared_color: "#3D405B"
        members:
          - name: Alex
            color: "#E07A5F"
            initial: A              # optional; defaults to first letter of name
            pattern: solid          # e-ink fill; solid | hatch | dots | outline
            calendars: [alex-personal, alex-work]
          - name: Kid
            color: "#81B29A"
            pattern: hatch
            calendars: [school, soccer]
        calendars:                  # identical schema to calendar-agenda (SPEC-007 §1)
          - name: alex-personal
            url_env: ALEX_CAL_URL
          - name: alex-work
            url_env: ALEX_WORK_CAL_URL
          - name: school
            url_env: SCHOOL_CAL_URL
          - name: soccer
            url: "https://example.org/soccer.ics"
          - name: holidays
            url: "https://calendar.google.com/…/basic.ics"
```

### Validation (boot and LKGC, SPEC-012)
- Every name in `members[].calendars` must match a `calendars[].name`. An unknown name fails validation. A typo would otherwise silently drop a person's events.
- Member names are unique; at most 8 members (the day view cannot fit more columns legibly at 6×2).
- `pattern` must be distinct across members when the widget can render on e-ink, because colour is not available there.
- `hours[0] < hours[1]`.
- A per-calendar `color` is ignored when a member claims the calendar; the member's colour wins. It still applies to unclaimed calendars if set; otherwise `shared_color` is used.

### Manifest
```yaml
name: calendar-family
version: "1.0.0"
description: Family wall calendar with per-member colour coding and week, day and month views.
provider: calendar-agenda
capabilities:
  - ambient-static
  - touch-interactive
default_dimensions: [6, 2]
supported_dimensions:
  - [4, 2]
  - [6, 2]
```
Sizes below 4×2 are not supported. At those sizes the widget would degrade into an agenda list, which `calendar-agenda` already is.

---

## Views

### Week (default)
- `days` columns, today highlighted, weekend columns tinted.
- An all-day band at the top of each column. A multi-day event spans its columns as one bar.
- Timed events stack in start order as blocks: time, title, owner initial(s). Blocks are not positioned proportionally by hour in week view; stacking keeps a dense day readable.
- If a column overflows, the last visible row becomes `+N more`.
- Header line: week range (`Sep 27 – Oct 3`). The fixed screen header already shows weather (SPEC-007 §4), so the widget does not repeat it.

### Day
- One column per member, plus a `Shared` column if any unclaimed event exists that day.
- Hour grid across `hours`. Blocks are positioned by time, so overlapping commitments between people line up horizontally.
- A shared event spans the columns of all its owners when those columns are adjacent. Otherwise it is repeated in each owner's column with a link glyph.

### Month
- Six-week grid. Each day shows one dot per member with events that day, in member order, and the first event's title if it fits.
- `today` is outlined.

### Size adaptation

| | 6×2 | 4×2 |
|---|---|---|
| `days: auto` | 7 | 5 (today + 4) |
| Week block content | time · title · initials | time · initials; title truncated to one line |
| Day columns | up to 8 members | up to 3 members; further members share a column, distinguished by colour and initial |
| Month cell | dots + first title | dots only |

An explicit `days: 7` at 4×2 is allowed. Titles are then hidden, and a tap reveals them.

---

## Touch Interaction (Profile A, SPEC-010)
All interaction is client-side and read-only. No mutation endpoints are called (SPEC-003 `touch-interactive`, SPEC-006).
- **Tap an event** to open a detail card: title, time, location, owners and description, first 280 characters. Tapping outside the card closes it.
- **Tap a day header** in week or month view to open that day's day view.
- **Swipe left or right** to page by `days` in week view, by one day in day view, and by one month in month view. Paging is limited to the fetched window. A page beyond the window shows its frame with a "not synced yet" note, and the widget widens the window on the next refresh.
- **View switcher:** three segmented buttons (Week, Day, Month) in the widget's header row. Touch targets follow `--mm-touch-target-min`.
- **Return to default:** 60 s after the last touch, the widget returns to `default_view` on the current date, so the wall never stays parked on some other week.

Screen rotation (SPEC-005) pauses while a detail card is open, and resumes when the card closes or on the 60 s return.

## E-Ink Rendering (Profile B, SPEC-009)
- **Week view only**, whatever `default_view` says. Day and month views need colour or fine detail that a 1-bit panel cannot render legibly.
- Colour is replaced by each member's `pattern`: a solid, hatched, dotted or outlined block, always with the initial printed inside. Unclaimed events use an outline with no initial.
- Today's column is marked with an inverted header, not a tint.
- The minimum text size is the panel's smallest legible size from SPEC-009. At 4×2 on 800×480, titles are dropped before the text size shrinks.
- Refresh follows SPEC-009: a `widget.update` marks the screen dirty, and SPEC-009's refresh coalescing absorbs the provider's 300 s cadence. The widget does not trigger refreshes on its own schedule.

## Styling
Uses the server-wide stylesheet (SPEC-003 §"Unified Semantic HTML & Server-Wide Styling"). Member colours are emitted as CSS custom properties on the widget root (`--mm-member-0`, `--mm-member-1`, …) rather than inline styles, so a custom stylesheet such as Lamplight can restyle blocks without template changes. Text on a member-coloured block is black or white, whichever has the higher contrast against that colour. The choice is made server-side and emitted as `--mm-member-N-text`.

## Privacy
Event titles and locations are visible to anyone in the room. That is inherent to a wall calendar, and it is why members claim calendars explicitly instead of the widget reading every calendar a feed exposes. Optional `private: true` on a `calendars[]` entry renders that calendar's events as `Busy`, keeping time and owner and dropping title, location and description. This is resolved in the view model, so private titles never reach the client.

## Testing
- View-model unit tests: member resolution, shared-event merge by id, unclaimed-calendar fallback, window computation for each view, and the `+N more` overflow.
- Config validation tests: unknown member calendar, duplicate member, member limit, duplicate e-ink patterns, and bad `hours`.
- Render tests for each view at 4×2 and 6×2 against fixed snapshots, including an e-ink 1-bit render at 800×480.
- Web tests: tap-to-detail, day-header navigation, swipe paging bounds, and the 60 s return to default.

## Open Questions
- **Member by attendee.** Should membership also resolve from an event's attendee emails, so a work invite lands on the right person without that person sharing a whole work calendar? This needs the provider to expose `ATTENDEE`, which `CalendarEvent` does not carry today.
- **Voice.** With SPEC-011 speaker identification, "what's on my calendar" could filter to the matched member. This belongs to the brain, not the widget, but the member-to-calendar mapping would need to be readable by it.
