import type { ComponentType } from 'react';

/** Props every widget component receives from WidgetHost. */
export interface WidgetComponentProps {
  id: string;
  type: string;
}

/**
 * Widget type -> component. Widget tasks add entries here; WidgetHost renders a
 * placeholder for any type missing from the map.
 */
export const WIDGET_COMPONENTS: Readonly<Record<string, ComponentType<WidgetComponentProps>>> = {};

export function widgetComponentFor(type: string | undefined): ComponentType<WidgetComponentProps> | undefined {
  if (type === undefined || !Object.prototype.hasOwnProperty.call(WIDGET_COMPONENTS, type)) return undefined;
  return WIDGET_COMPONENTS[type];
}
