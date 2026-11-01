import type {
  FamilyDay,
  FamilyMonthDay,
  FamilyMonthWeek,
  FamilySpanEvent,
  ViewMode,
} from './types';

export const SHORT_MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
export const SHORT_WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
export const FULL_WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
export const FULL_MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

export function parseISODate(s: string): Date {
  const parts = s.split('T')[0].split('-');
  const y = parseInt(parts[0], 10);
  const m = parseInt(parts[1], 10);
  const d = parseInt(parts[2], 10);
  return new Date(y, m - 1, d, 12, 0, 0);
}

export function formatISODate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

export const toISODate = formatISODate;

export function addDays(d: Date, n: number): Date {
  const res = new Date(d.getTime());
  res.setDate(res.getDate() + n);
  return res;
}

export function addMonths(d: Date, n: number): Date {
  const res = new Date(d.getTime());
  res.setDate(1);
  res.setMonth(res.getMonth() + n);
  return res;
}

export function startOfWeek(d: Date, weekStartsOn = 0): Date {
  const res = new Date(d.getTime());
  const day = res.getDay();
  const diff = (day < weekStartsOn ? 7 : 0) + day - weekStartsOn;
  res.setDate(res.getDate() - diff);
  res.setHours(12, 0, 0, 0);
  return res;
}

export function formatShortDate(isoDate: string): string {
  if (!isoDate || !/^\d{4}-\d{2}-\d{2}/.test(isoDate)) return '';
  const d = parseISODate(isoDate);
  if (Number.isNaN(d.getTime())) return '';
  return `${SHORT_WEEKDAYS[d.getDay()]}, ${SHORT_MONTHS[d.getMonth()]} ${d.getDate()}`;
}

export function formatDayLabel(d: Date): string {
  return `${FULL_WEEKDAYS[d.getDay()]}, ${SHORT_MONTHS[d.getMonth()]} ${d.getDate()}`;
}

export function formatRangeLabel(start: Date, end: Date): string {
  if (start.getFullYear() === end.getFullYear()) {
    if (start.getMonth() === end.getMonth()) {
      return `${SHORT_MONTHS[start.getMonth()]} ${start.getDate()} – ${end.getDate()}, ${start.getFullYear()}`;
    }
    return `${SHORT_MONTHS[start.getMonth()]} ${start.getDate()} – ${SHORT_MONTHS[end.getMonth()]} ${end.getDate()}, ${start.getFullYear()}`;
  }
  return `${SHORT_MONTHS[start.getMonth()]} ${start.getDate()}, ${start.getFullYear()} – ${SHORT_MONTHS[end.getMonth()]} ${end.getDate()}, ${end.getFullYear()}`;
}

export const formatWeekLabel = formatRangeLabel;

export function formatMonthLabel(d: Date): string {
  return `${FULL_MONTHS[d.getMonth()]} ${d.getFullYear()}`;
}

export function formatModalDateRange(startDate?: string, endDate?: string): string {
  const startFmt = startDate ? formatShortDate(startDate) : '';
  const endFmt = endDate && endDate !== startDate ? formatShortDate(endDate) : '';
  if (startFmt && endFmt) return `${startFmt} – ${endFmt}`;
  return startFmt || endFmt;
}

export const formatModalDateLabel = formatModalDateRange;

export function stripLeadingMealieEmoji(title?: string): string {
  if (!title) return '';
  return title.replace(/^(?:\u{1F37D}\uFE0F?\s*)+/u, '');
}

export const stripMealiePrefix = stripLeadingMealieEmoji;

export function sliceWeekWindow(
  allDays: FamilyDay[] | undefined,
  activeView: ViewMode,
  currentDateISO: string
): FamilyDay[] {
  const days = allDays || [];
  if (days.length === 0) return [];
  if (activeView === 'day') {
    const match = days.find((d) => d.date === currentDateISO);
    return match ? [match] : [days[0]];
  }
  return days;
}

export function computeDayLayout(
  allDayEvents: FamilySpanEvent[] | undefined,
  days: FamilyDay[] | undefined,
  activeView: ViewMode,
  currentDateISO: string
): FamilySpanEvent[] {
  if (!allDayEvents || allDayEvents.length === 0) return [];
  if (activeView !== 'day') return allDayEvents;
  const dayIdx = (days || []).findIndex((d) => d.date === currentDateISO);
  if (dayIdx < 0) return [];
  return allDayEvents.filter(
    (ev) => ev.start_col <= dayIdx + 1 && ev.start_col + ev.col_span > dayIdx + 1
  );
}

export function buildMonthViewFromDays(days: FamilyMonthDay[]): FamilyMonthWeek[] {
  const weeks: FamilyMonthWeek[] = [];
  for (let i = 0; i < days.length; i += 7) {
    weeks.push({
      week_number: Math.floor(i / 7) + 1,
      days: days.slice(i, i + 7),
    });
  }
  return weeks;
}
