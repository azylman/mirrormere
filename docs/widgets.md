# Widgets

## Rendering under the React UI

A widget package is a directory with `manifest.yaml` and `views/widget.html`, plus an optional `assets/`
directory. Built-in packages live in `widgets/`, custom ones in `/config/widgets/`. Both the legacy page
(`/display`) and the React shell (`/display?ui=react`) render the same server fragment from
`GET api/widgets/{id}/render`.

The React shell looks up a component for each widget type in `WIDGET_COMPONENTS`
(`web/ui/src/widgets/registry.ts`). A type with no entry renders through `LegacyWidget`
(`web/ui/src/widgets/LegacyWidget.tsx`), so a custom package with only an HTML template works without
any React code. `LegacyWidget`:

- fetches the fragment on mount and again when the widget's `timestamp` changes (the `widget.update`
  signal). It passes the page's `node` query parameter, as the legacy page does. Each request has its own
  `AbortController`, and the previous one is aborted before a new fetch and on unmount.
- while its pane is not current, keeps its content and does not re-fetch on update. It fetches once when
  the pane becomes current, if the timestamp changed meanwhile.
- inserts the fragment's `.widget-card` into a container it owns, with `replaceChildren`. React never
  reconciles the legacy DOM. Sibling `<style>` and `<script>` nodes move into the card. A
  `<script type="application/json">` block stays in the card as inert data for controllers to read.
- replaces a `<img onload="window.MirrormereX.mount(this.closest('...'))">` hook with an explicit call.
  The first global named in the hook that is defined gets `mount(element)` after the card is in the
  document, and `unmount(widgetId)` before the card is replaced or the cell unmounts. The `onload`
  attribute is removed, so the controller is not mounted twice. A global that is not defined is skipped.
- adds a stylesheet linked from the fragment under the type's `assets_base` (`widget-types/<type>/assets/`)
  to the document head, once per type. Later cells of the same type add no second link.

The cell around the fragment keeps the grid placement. `web/ui/src/styles.css` strips the cell's own card
styling when it holds a `.widget-legacy` container, so the fragment's card is not drawn twice.

## Shipping a React component for a built-in type

Register a component for the type in `WIDGET_COMPONENTS`. `WidgetHost` passes `id` and `type`, and the
component reads its data from `useWidget(id)`. Use `useIsCurrentPane()` to pause timers and decoders while
the pane is off-screen. Keep the card markup inside the component, because `WidgetHost` already renders
the `.widget-card` cell around it. Components need tests (`npm test` in `web/ui/`).
