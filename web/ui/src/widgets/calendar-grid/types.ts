export interface FamilyMember {
  name: string;
  initial: string;
  color: string;
  text_color: string;
  pattern: string;
  calendars?: string[];
}

export interface FamilyEvent {
  id: string;
  title: string;
  location?: string | undefined;
  image_url?: string;
  description?: string | undefined;
  start?: string | undefined;
  end?: string | undefined;
  all_day?: boolean;
  owners?: string[] | undefined;
  initials?: string[];
  colors?: string[];
  color?: string | undefined;
  text_color?: string;
  patterns?: string[];
  pattern?: string;
}

export interface FamilySpanEvent extends FamilyEvent {
  start_col: number;
  col_span: number;
}

export interface FamilyDayEvent extends FamilyEvent {
  top_pct: number;
  height_pct: number;
  left_pct: number;
  width_pct: number;
  is_early_edge?: boolean;
  is_late_edge?: boolean;
  col_span?: number;
  link_glyph?: string;
  is_linked?: boolean;
}

export interface TimelineMarker {
  label: string;
  top_pct: number;
  is_start?: boolean;
  is_end?: boolean;
}

export interface CollapsedHoursGap {
  start_hour: number;
  end_hour: number;
  start_str?: string;
  end_str?: string;
  label: string;
  top_pct: number;
  height_pct: number;
}

export interface FamilyDay {
  date: string;
  weekday_abbr?: string;
  day_of_month: number;
  is_today: boolean;
  is_weekend: boolean;
  special_label?: string;
  all_day?: FamilyEvent[] | undefined;
  meals?: FamilyEvent[] | undefined;
  timed?: FamilyEvent[] | undefined;
  overflow?: number;
  events?: FamilyDayEvent[] | undefined;
  early_events?: FamilyDayEvent[] | undefined;
  late_events?: FamilyDayEvent[] | undefined;
}

export interface FamilyViewModel {
  range_start: string;
  range_end: string;
  range_label: string;
  today: string;
  /** '12h' or '24h' in practice; any other value falls back to 24h. */
  time_format?: string;
  hours_start?: string | undefined;
  hours_end?: string | undefined;
  hour_markers?: string[];
  timeline_markers?: TimelineMarker[];
  collapsed_gaps?: CollapsedHoursGap[];
  members?: FamilyMember[];
  days?: FamilyDay[];
  all_day_events?: FamilySpanEvent[];
  shared_color?: string;
  cached_range_start?: string;
  cached_range_end?: string;
}

interface FamilyMonthDot {
  name: string;
  initial: string;
  color: string;
  text_color: string;
  pattern: string;
  is_shared: boolean;
  member_index: number;
}

export interface FamilyMonthDay {
  date: string;
  day_of_month: number;
  weekday_abbr?: string;
  is_current_month: boolean;
  is_today: boolean;
  is_weekend: boolean;
  special_label?: string;
  dots?: FamilyMonthDot[] | undefined;
  event_title?: string | undefined;
  events_count?: number;
  events?: FamilyEvent[];
}

export interface FamilyMonthWeek {
  week_number: number;
  days: FamilyMonthDay[];
}

export interface FamilyMonthViewModel {
  month_label: string;
  month_name: string;
  year: number;
  month: number;
  range_start: string;
  range_end: string;
  today: string;
  /** Go nil slice: serialised as null when the month view is not populated. */
  weekday_headers?: string[] | null;
  members?: FamilyMember[];
  weeks?: FamilyMonthWeek[] | undefined;
  days?: FamilyMonthDay[] | undefined;
  shared_color?: string;
  cached_range_start?: string;
  cached_range_end?: string;
}

export interface CalendarGridViewModel {
  week?: FamilyViewModel;
  month?: FamilyMonthViewModel;
  active_view?: string;
  cached_range_start?: string;
  cached_range_end?: string;
}

export type ViewMode = 'day' | 'week' | 'month';
