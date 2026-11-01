import type { ComponentType } from 'react';
import { LegacyWidget } from './LegacyWidget';
import { SpacerWidget } from './spacer/SpacerWidget';
import { TasksWidget } from './tasks/TasksWidget';
import { WeatherForecastWidget } from './weather-forecast/WeatherForecastWidget';
import { ChatLogWidget } from './chat-log/ChatLogWidget';
import { CalendarFeedWidget } from './calendar-feed/CalendarFeedWidget';
import { CalendarGridWidget } from './calendar-grid/CalendarGridWidget';
import { PhotoCarouselWidget } from './photo-carousel/PhotoCarouselWidget';
import { LiveViewWidget } from './live-view/LiveViewWidget';

/** Props every widget component receives from WidgetHost. */
export interface WidgetComponentProps {
  id: string;
  type: string;
}

/**
 * Widget type -> component. Widget tasks add entries here. A type missing from the map
 * renders through LEGACY_WIDGET_FALLBACK.
 */
export const WIDGET_COMPONENTS: Readonly<Record<string, ComponentType<WidgetComponentProps>>> = {
  spacer: SpacerWidget,
  tasks: TasksWidget,
  'weather-forecast': WeatherForecastWidget,
  'chat-log': ChatLogWidget,
  'calendar-feed': CalendarFeedWidget,
  'calendar-grid': CalendarGridWidget,
  'photo-carousel': PhotoCarouselWidget,
  'live-view': LiveViewWidget,
};

/**
 * Renders any type with no registered component: a custom or not-yet-ported package, served as
 * its legacy HTML fragment.
 */
export const LEGACY_WIDGET_FALLBACK: ComponentType<WidgetComponentProps> = LegacyWidget;

export function widgetComponentFor(type: string | undefined): ComponentType<WidgetComponentProps> | undefined {
  if (type === undefined || !Object.prototype.hasOwnProperty.call(WIDGET_COMPONENTS, type)) return undefined;
  return WIDGET_COMPONENTS[type];
}
