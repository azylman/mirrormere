/**
 * Shared formatting and SVG helpers for Mirrormere widgets.
 * Ported from internal/render/helpers.go for native React widgets.
 */

export interface TasksStatsResult {
  total: number;
  completed: number;
  remaining: number;
  overdue: number;
}

/** Escapes XML/HTML special characters for safe inclusion in SVG markup. */
export function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Calculates task completion metrics (total, completed, remaining, overdue).
 * Accepts an array of task items or an object containing an items/list array.
 * Supports both snake_case (done, due_date) and PascalCase (Done, DueDate).
 */
export function tasksStats(input: unknown, now: Date = new Date()): TasksStatsResult {
  if (!input) {
    return { total: 0, completed: 0, remaining: 0, overdue: 0 };
  }

  let items: unknown[] | null = null;
  if (Array.isArray(input)) {
    items = input;
  } else if (typeof input === 'object') {
    const obj = input as Record<string, unknown>;
    const candidate = obj.items ?? obj.Items ?? obj.list ?? obj.List;
    if (Array.isArray(candidate)) {
      items = candidate;
    }
  }

  if (!items) {
    return { total: 0, completed: 0, remaining: 0, overdue: 0 };
  }

  let total = 0;
  let completed = 0;
  let overdue = 0;

  const nowUTC = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
  const nowMs = now.getTime();

  for (const item of items) {
    if (!item || typeof item !== 'object') continue;
    const task = item as Record<string, unknown>;

    // Done check
    const doneVal = task.done ?? task.Done;
    const isDone = Boolean(doneVal);
    total++;
    if (isDone) {
      completed++;
      continue;
    }

    // Overdue check on pending tasks
    const rawDue = task.due_date ?? task.DueDate ?? task.due ?? task.Due;
    if (typeof rawDue === 'string' && rawDue.trim() !== '') {
      const dueStr = rawDue.trim();
      if (dueStr.length === 10 && /^\d{4}-\d{2}-\d{2}$/.test(dueStr)) {
        const [y, m, d] = dueStr.split('-').map(Number);
        const parsedUTC = Date.UTC(y, m - 1, d);
        if (nowUTC > parsedUTC) {
          overdue++;
        }
      } else {
        const parsed = new Date(dueStr);
        if (!isNaN(parsed.getTime()) && nowMs > parsed.getTime()) {
          overdue++;
        }
      }
    }
  }

  return {
    total,
    completed,
    remaining: Math.max(total - completed, 0),
    overdue,
  };
}

/**
 * Formats a time value according to Go layout string ('3 PM' or '15:04').
 * Supports ISO/RFC3339 strings and 'YYYY-MM-DDTHH:mm' strings.
 * Returns '' on invalid input or date-only inputs ('YYYY-MM-DD').
 */
export function formatTime(layout: string, value: unknown): string {
  if (value === null || value === undefined || value === '') return '';

  let date: Date | null = null;
  let hour = -1;
  let minute = 0;

  if (typeof value === 'string') {
    const s = value.trim();
    // Go formatTime returns "" on date-only input (length 10: YYYY-MM-DD)
    if (/^\d{4}-\d{2}-\d{2}$/.test(s)) {
      return '';
    }

    // Match ISO with optional timezone
    const isoMatch = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?(?:\.\d+)?(Z|[+-]\d{2}:?\d{2})?$/.exec(s);
    if (isoMatch) {
      const tz = isoMatch[7];
      if (tz === 'Z') {
        const d = new Date(s);
        if (isNaN(d.getTime())) return '';
        hour = d.getUTCHours();
        minute = d.getUTCMinutes();
      } else if (tz && tz !== '') {
        const d = new Date(s);
        if (isNaN(d.getTime())) return '';
        // In Go, FormatTimeInLocation preserves the local time of the parsed timestamp string
        // unless a location override is given.
        hour = Number(isoMatch[4]);
        minute = Number(isoMatch[5]);
      } else {
        hour = Number(isoMatch[4]);
        minute = Number(isoMatch[5]);
      }
    } else {
      // Check for standalone HH:mm
      const timeMatch = /^(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(s);
      if (timeMatch) {
        hour = Number(timeMatch[1]);
        minute = Number(timeMatch[2]);
      } else {
        const d = new Date(s);
        if (isNaN(d.getTime())) return '';
        date = d;
      }
    }
  } else if (value instanceof Date) {
    if (isNaN(value.getTime())) return '';
    date = value;
  } else if (typeof value === 'number') {
    const d = new Date(value);
    if (isNaN(d.getTime())) return '';
    date = d;
  } else {
    return '';
  }

  if (date !== null) {
    hour = date.getHours();
    minute = date.getMinutes();
  }

  if (hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return '';
  }

  if (layout === '3 PM') {
    const ampm = hour >= 12 ? 'PM' : 'AM';
    const h12 = hour % 12 === 0 ? 12 : hour % 12;
    return `${h12} ${ampm}`;
  }

  if (layout === '15:04' || layout === 'HH:mm') {
    return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`;
  }

  return '';
}

/**
 * Extracts today's maximum precipitation probability percentage (0-100)
 * from daily forecast, hourly forecast, current snapshot, or map data.
 */
export function todayPrecipProb(data: unknown): number {
  if (!data || typeof data !== 'object') {
    return 0;
  }

  const d = data as Record<string, unknown>;

  // Daily array
  const daily = d.daily ?? d.Daily;
  if (Array.isArray(daily) && daily.length > 0) {
    const first = daily[0];
    if (first && typeof first === 'object') {
      const f = first as Record<string, unknown>;
      const p = f.precip_prob_max ?? f.PrecipProbMax ?? f.precip_prob ?? f.PrecipitationProbability;
      if (typeof p === 'number' && !isNaN(p)) return Math.max(0, Math.min(100, Math.round(p)));
    }
  }

  // Hourly array (max across first 12 hours)
  const hourly = d.hourly ?? d.Hourly;
  if (Array.isArray(hourly) && hourly.length > 0) {
    let maxProb = 0;
    for (const item of hourly.slice(0, 12)) {
      if (item && typeof item === 'object') {
        const h = item as Record<string, unknown>;
        const p = h.precip_prob ?? h.PrecipProb ?? h.precip_prob_max ?? h.PrecipitationProbability;
        if (typeof p === 'number' && !isNaN(p)) {
          maxProb = Math.max(maxProb, Math.round(p));
        }
      }
    }
    if (maxProb > 0) {
      return Math.max(0, Math.min(100, maxProb));
    }
  }

  // Current
  const current = d.current ?? d.Current;
  if (current && typeof current === 'object') {
    const c = current as Record<string, unknown>;
    const p = c.precip_prob_max ?? c.PrecipProbMax ?? c.precip_prob ?? c.PrecipitationProbability;
    if (typeof p === 'number' && !isNaN(p)) return Math.max(0, Math.min(100, Math.round(p)));
  }

  // Direct precip_prob_max
  const direct = d.precip_prob_max ?? d.PrecipProbMax;
  if (typeof direct === 'number' && !isNaN(direct)) {
    return Math.max(0, Math.min(100, Math.round(direct)));
  }

  return 0;
}

/** Returns the inner SVG elements for a WMO weather condition token. */
export function weatherIconPath(token: string): string {
  switch (token) {
    case 'weather-sunny':
      return '<circle cx="12" cy="12" r="5" fill="#f59e0b" stroke="#f59e0b" stroke-width="1"/><g stroke="#f59e0b" stroke-width="2" stroke-linecap="round"><line x1="12" y1="1.5" x2="12" y2="3.5"/><line x1="12" y1="20.5" x2="12" y2="22.5"/><line x1="4.5" y1="4.5" x2="6" y2="6"/><line x1="18" y1="18" x2="19.5" y2="19.5"/><line x1="1.5" y1="12" x2="3.5" y2="12"/><line x1="20.5" y1="12" x2="22.5" y2="12"/><line x1="4.5" y1="19.5" x2="6" y2="18"/><line x1="18" y1="6" x2="19.5" y2="4.5"/></g>';
    case 'weather-clear-night':
      return '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>';
    case 'weather-partly-cloudy':
      return '<circle cx="16" cy="9" r="4" fill="#f59e0b"/><g stroke="#f59e0b" stroke-width="1.8" stroke-linecap="round"><line x1="16" y1="2" x2="16" y2="3.5"/><line x1="21.5" y1="6" x2="20" y2="7"/><line x1="21.5" y1="12" x2="20" y2="11"/><line x1="11.5" y1="4" x2="12.5" y2="5"/></g><path d="M15.5 19H7a4.5 4.5 0 0 1-.8-8.9A5.5 5.5 0 0 1 16.8 11 3.5 3.5 0 0 1 15.5 19z" fill="#ffffff" stroke="#94a3b8" stroke-width="1.6" stroke-linejoin="round"/>';
    case 'weather-night-partly-cloudy':
      return '<path d="M19 10.5A6 6 0 0 1 12.5 4a5 5 0 0 0 6.5 6.5z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.5" stroke-linejoin="round"/><path d="M15.5 19H7a4.5 4.5 0 0 1-.8-8.9A5.5 5.5 0 0 1 16.8 11 3.5 3.5 0 0 1 15.5 19z" fill="#ffffff" stroke="#94a3b8" stroke-width="1.6" stroke-linejoin="round"/>';
    case 'weather-cloudy':
      return '<path d="M17.5 19H7a5 5 0 0 1-1-9.9A6 6 0 0 1 17.8 11 4 4 0 0 1 17.5 19z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/>';
    case 'weather-fog':
      return '<g stroke="#94a3b8" stroke-width="2" stroke-linecap="round"><line x1="4" y1="10" x2="20" y2="10"/><line x1="3" y1="14" x2="21" y2="14"/><line x1="5" y1="18" x2="19" y2="18"/><line x1="7" y1="6" x2="17" y2="6"/></g>';
    case 'weather-rainy':
      return '<path d="M17.5 15H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 7 3.5 3.5 0 0 1 17.5 15z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g stroke="#38bdf8" stroke-width="2" stroke-linecap="round"><line x1="8" y1="18" x2="7" y2="21"/><line x1="12" y1="18" x2="11" y2="21"/><line x1="16" y1="18" x2="15" y2="21"/></g>';
    case 'weather-pouring':
      return '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><g stroke="#0284c7" stroke-width="2" stroke-linecap="round"><line x1="6" y1="17" x2="4" y2="21"/><line x1="10" y1="17" x2="8" y2="21"/><line x1="14" y1="17" x2="12" y2="21"/><line x1="18" y1="17" x2="16" y2="21"/></g>';
    case 'weather-snowy':
      return '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g fill="#93c5fd"><circle cx="8" cy="18" r="1.3"/><circle cx="12" cy="18" r="1.3"/><circle cx="16" cy="18" r="1.3"/><circle cx="10" cy="21" r="1.3"/><circle cx="14" cy="21" r="1.3"/></g>';
    case 'weather-snowy-rainy':
      return '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g fill="#93c5fd"><circle cx="8" cy="18" r="1.3"/><circle cx="14" cy="18" r="1.3"/></g><g stroke="#38bdf8" stroke-width="2" stroke-linecap="round"><line x1="11" y1="18" x2="10" y2="21"/></g>';
    case 'weather-lightning':
    case 'weather-lightning-rainy':
      return '<path d="M17.5 13H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 5 3.5 3.5 0 0 1 17.5 13z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><polygon points="12 13 9 18 13 18 11 23 16 16 12 16 14 13" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linejoin="round"/>';
    default:
      return '<path d="M17.5 19H7a5 5 0 0 1-1-9.9A6 6 0 0 1 17.8 11 4 4 0 0 1 17.5 19z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/>';
  }
}

/**
 * Returns a sanitized scalable vector inline SVG string for WMO condition tokens.
 * Matches WeatherIcon in internal/render/helpers.go.
 */
export function weatherIconSvg(iconName?: string | null, size: number = 20): string {
  const token = typeof iconName === 'string' && iconName.trim() !== '' ? iconName.trim() : 'weather-cloudy';
  let s = typeof size === 'number' && !isNaN(size) && size > 0 ? Math.round(size) : 20;
  if (s < 12) s = 12;
  else if (s > 64) s = 64;

  const pathHTML = weatherIconPath(token);
  return `<svg width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mm-weather-icon mm-icon-${escapeHtml(token)}">${pathHTML}</svg>`;
}

export interface WeatherHourlyGraphOptions {
  svgHeight?: number;
  curveTop?: number;
  curveBottom?: number;
  baseY?: number;
}

interface HourlyPoint {
  timeStr: string;
  temp: number;
  precipProb: number;
  icon: string;
  x: number;
  y: number;
}

/**
 * Produces an SVG continuous line graph showing hourly temperature trends,
 * condition icons, precipitation chances, and time throughout the day.
 * Matches WeatherHourlyGraph in internal/render/helpers.go.
 */
export function weatherHourlyGraphSvg(
  hourly: unknown,
  widgetId?: string,
  options?: WeatherHourlyGraphOptions,
): string {
  if (!hourly || !Array.isArray(hourly) || hourly.length === 0) {
    return '';
  }

  // Extract up to 8 points
  const rawPts = hourly.slice(0, 8);
  const pts: HourlyPoint[] = [];

  for (const item of rawPts) {
    if (!item || typeof item !== 'object') continue;
    const m = item as Record<string, unknown>;

    let timeStr = '';
    const rawTime = m.time ?? m.Time;
    if (typeof rawTime === 'string') {
      timeStr = rawTime;
    }

    let temp = Number(m.temp ?? m.Temp);
    if (isNaN(temp) || !isFinite(temp)) {
      temp = 0.0;
    }

    let precipProb = 0;
    const rawPrecip = m.precip_prob ?? m.PrecipProb;
    if (typeof rawPrecip === 'number' && !isNaN(rawPrecip)) {
      precipProb = Math.round(rawPrecip);
    }

    let icon = 'weather-cloudy';
    const rawIcon = m.icon ?? m.Icon;
    if (typeof rawIcon === 'string' && rawIcon.trim() !== '') {
      icon = rawIcon.trim();
    }

    pts.push({ timeStr, temp, precipProb, icon, x: 0, y: 0 });
  }

  if (pts.length === 0) {
    return '';
  }

  const n = pts.length;
  const gradientID = widgetId && widgetId.trim() !== ''
    ? `mm-weather-grad-${escapeHtml(widgetId.trim())}`
    : 'mm-weather-hourly-grad';

  const baseColWidth = 68.0;
  const minGraphWidth = 560.0;
  const svgHeight = options?.svgHeight ?? 108.0;
  const curveTop = options?.curveTop ?? 34.0;
  const curveBottom = options?.curveBottom ?? 78.0;
  const baseY = options?.baseY ?? (svgHeight === 300.0 ? 240.0 : 90.0);

  const isTall = svgHeight >= 200.0;

  let colWidth = baseColWidth;
  if (n * colWidth < minGraphWidth && n > 0) {
    colWidth = minGraphWidth / n;
  }
  const totalWidth = n * colWidth;

  let minTemp = pts[0].temp;
  let maxTemp = pts[0].temp;
  for (let i = 1; i < n; i++) {
    if (pts[i].temp < minTemp) minTemp = pts[i].temp;
    if (pts[i].temp > maxTemp) maxTemp = pts[i].temp;
  }

  let span = maxTemp - minTemp;
  if (span < 4.0) {
    const mid = (minTemp + maxTemp) / 2.0;
    minTemp = mid - 2.0;
    span = 4.0;
  }

  for (let i = 0; i < n; i++) {
    pts[i].x = i * colWidth + colWidth / 2.0;
    const ratio = (pts[i].temp - minTemp) / span;
    pts[i].y = curveBottom - ratio * (curveBottom - curveTop);
  }

  let svg = `<svg class="weather-hourly-graph" viewBox="0 0 ${totalWidth.toFixed(1)} ${svgHeight.toFixed(1)}" style="height: 100%; width: auto; min-width: ${totalWidth.toFixed(1)}px; flex-shrink: 0; display: block; overflow: visible;">`;
  svg += `<defs><linearGradient id="${gradientID}" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="#ff6f59" stop-opacity="0.25"/><stop offset="100%" stop-color="#ff6f59" stop-opacity="0.0"/></linearGradient></defs>`;
  svg += `<line x1="0" y1="${baseY.toFixed(1)}" x2="${totalWidth.toFixed(1)}" y2="${baseY.toFixed(1)}" stroke="rgba(90, 60, 30, 0.08)" stroke-width="1"/>`;

  if (n > 1) {
    let curvePath = `M ${pts[0].x.toFixed(1)} ${pts[0].y.toFixed(1)}`;
    let areaPath = `M ${pts[0].x.toFixed(1)} ${baseY.toFixed(1)} L ${pts[0].x.toFixed(1)} ${pts[0].y.toFixed(1)}`;

    for (let i = 0; i < n - 1; i++) {
      const dx = (pts[i + 1].x - pts[i].x) / 2.0;
      const seg = ` C ${(pts[i].x + dx).toFixed(1)} ${pts[i].y.toFixed(1)}, ${(pts[i + 1].x - dx).toFixed(1)} ${pts[i + 1].y.toFixed(1)}, ${pts[i + 1].x.toFixed(1)} ${pts[i + 1].y.toFixed(1)}`;
      curvePath += seg;
      areaPath += seg;
    }
    areaPath += ` L ${pts[n - 1].x.toFixed(1)} ${baseY.toFixed(1)} Z`;

    svg += `<path d="${areaPath}" fill="url(#${gradientID})"/>`;
    svg += `<path d="${curvePath}" fill="none" stroke="var(--mm-coral, #ff6f59)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>`;
  }

  for (let i = 0; i < n; i++) {
    const pt = pts[i];
    const lineTopOffset = isTall ? 6.0 : 4.0;
    svg += `<line x1="${pt.x.toFixed(1)}" y1="${(pt.y + lineTopOffset).toFixed(1)}" x2="${pt.x.toFixed(1)}" y2="${baseY.toFixed(1)}" stroke="rgba(90, 60, 30, 0.08)" stroke-dasharray="2 2"/>`;

    if (pt.precipProb > 0) {
      const precipY = isTall ? 20 : 12;
      svg += `<text x="${pt.x.toFixed(1)}" y="${precipY}" text-anchor="middle" font-size="11" font-weight="700" fill="var(--mm-sky, #3b9edd)" style="user-select: none; pointer-events: none;">☂ ${pt.precipProb}%</text>`;
    }

    const iconPath = weatherIconPath(pt.icon);
    const iconY = isTall ? 32 : 14;
    const iconScale = isTall ? 1.2 : 0.8;
    const iconOffsetX = isTall ? 14.4 : 9.6;
    svg += `<g transform="translate(${(pt.x - iconOffsetX).toFixed(1)}, ${iconY}) scale(${iconScale})" fill="none" stroke="var(--mm-honey, #f59e0b)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${iconPath}</g>`;

    // Round half-away-from-zero to match Go math.Round
    const tempRound = Math.sign(pt.temp) * Math.round(Math.abs(pt.temp)) || 0;
    const tempY = isTall ? pt.y - 10.0 : pt.y - 6.0;
    const tempFontSize = isTall ? 14 : 12;
    svg += `<text x="${pt.x.toFixed(1)}" y="${tempY.toFixed(1)}" text-anchor="middle" font-size="${tempFontSize}" font-weight="800" fill="var(--mm-text-title, #2a2338)" style="user-select: none; pointer-events: none;">${tempRound}°</text>`;

    const circleR = isTall ? '4.0' : '3.5';
    svg += `<circle cx="${pt.x.toFixed(1)}" cy="${pt.y.toFixed(1)}" r="${circleR}" fill="#ffffff" stroke="var(--mm-coral, #ff6f59)" stroke-width="2.5"/>`;

    let timeLabel = 'Now';
    let timeWeight = '800';
    let timeColor = 'var(--mm-coral, #ff6f59)';
    if (i > 0) {
      timeWeight = '600';
      timeColor = 'var(--mm-text-dim, #8b819f)';
      const formatted = formatTime('3 PM', pt.timeStr);
      timeLabel = formatted !== '' ? formatted : pt.timeStr;
    }
    const timeY = isTall ? 275 : 102;
    const timeFontSize = isTall ? 13 : 11;
    svg += `<text x="${pt.x.toFixed(1)}" y="${timeY}" text-anchor="middle" font-size="${timeFontSize}" font-weight="${timeWeight}" fill="${timeColor}" style="user-select: none; pointer-events: none;">${escapeHtml(timeLabel)}</text>`;
  }

  svg += '</svg>';
  return svg;
}
