package render

import "context"

// RenderWidget executes the HTML template for the given widget instance, returning the rendered HTML fragment.
func (e *Engine) RenderWidget(ctx context.Context, widgetID string) ([]byte, error) {
	return e.RenderWidgetForNode(ctx, widgetID, "")
}
