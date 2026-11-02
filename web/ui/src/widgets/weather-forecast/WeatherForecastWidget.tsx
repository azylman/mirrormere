import {
  DEFAULT_SCHOOL_DAY_THRESHOLDS,
  type DaypartSlot,
  type SchoolDayThresholds,
  resolveCharacterPackSpriteUrl,
  resolveClothingConditionState,
  selectDailyVariantIndex,
} from './schoolDayPack';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import { todayPrecipProb } from '../format';
import { WeatherIcon } from './WeatherIcon';
import { WeatherHourlyGraph } from './WeatherHourlyGraph';

interface SlotWeatherInfo {
  temp: number;
  precipProb: number;
  icon: string;
}

const DAYPART_SLOTS: DaypartSlot[] = ['morning', 'afternoon', 'evening'];

function resolveSlotWeather(
  slot: DaypartSlot,
  data: Record<string, unknown> | undefined,
  current: Record<string, unknown> | undefined,
  daily: unknown[] | undefined,
  hourly: unknown[] | undefined,
): SlotWeatherInfo {
  const slots = data?.slots as Record<string, Record<string, unknown>> | undefined;
  const slotObj = slots?.[slot] ?? (data?.[slot] as Record<string, unknown> | undefined);

  if (slotObj) {
    const rawTemp = slotObj.temp ?? slotObj.temperature ?? 0;
    const rawPrecip = slotObj.precip_prob ?? slotObj.precip ?? 0;
    const icon = String(slotObj.icon ?? 'weather-sunny');
    return {
      temp: Math.round(Number(rawTemp)),
      precipProb: Math.round(Number(rawPrecip)),
      icon,
    };
  }

  if (Array.isArray(hourly) && hourly.length > 0) {
    const targetHour = slot === 'morning' ? 8 : slot === 'afternoon' ? 14 : 18;
    let bestItem: Record<string, unknown> | undefined;
    let bestDiff = Infinity;

    for (const item of hourly) {
      if (!item || typeof item !== 'object') continue;
      const h = item as Record<string, unknown>;
      const timeStr = String(h.time ?? h.Time ?? '');
      const match = timeStr.match(/(?:T|\s|^)(\d{1,2}):/);
      if (match) {
        const hour = parseInt(match[1], 10);
        const diff = Math.abs(hour - targetHour);
        if (diff < bestDiff) {
          bestDiff = diff;
          bestItem = h;
        }
      }
    }

    if (!bestItem) {
      const idx =
        slot === 'morning'
          ? 0
          : slot === 'afternoon'
            ? Math.floor(hourly.length / 2)
            : hourly.length - 1;
      bestItem = (hourly[idx] ?? {}) as Record<string, unknown>;
    }

    const rawTemp = bestItem.temp ?? bestItem.Temp ?? 0;
    const rawPrecip = bestItem.precip_prob ?? bestItem.PrecipProb ?? 0;
    const icon = String(bestItem.icon ?? bestItem.Icon ?? 'weather-partly-cloudy');
    return {
      temp: Math.round(Number(rawTemp)),
      precipProb: Math.round(Number(rawPrecip)),
      icon,
    };
  }

  const rawCurTemp = current?.temperature ?? current?.Temperature ?? 0;
  const curTemp = Number(rawCurTemp) || 0;

  const today =
    Array.isArray(daily) && daily.length > 0 && typeof daily[0] === 'object'
      ? (daily[0] as Record<string, unknown>)
      : undefined;
  const rawMax = today?.temp_max ?? today?.TempMax;
  const rawMin = today?.temp_min ?? today?.TempMin;
  const hasDaily = rawMax !== undefined && rawMin !== undefined;
  const maxTemp = hasDaily ? Number(rawMax) : curTemp;
  const minTemp = hasDaily ? Number(rawMin) : curTemp;

  let temp = curTemp;
  if (hasDaily) {
    if (slot === 'morning') temp = minTemp;
    else if (slot === 'afternoon') temp = maxTemp;
    else temp = (minTemp + maxTemp) / 2;
  }

  const precipProb = todayPrecipProb(data);
  const icon = String(current?.icon ?? current?.Icon ?? 'weather-partly-cloudy');

  return { temp: Math.round(temp), precipProb, icon };
}

/**
 * Native React widget component for weather forecasts.
 * Ports widgets/weather-forecast/views/widget.html.
 */
export function WeatherForecastWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const config = entry?.meta?.config as Record<string, unknown> | undefined;
  const data = entry?.data as Record<string, unknown> | undefined;
  const isError = entry?.state === 'error';

  const currentCandidate =
    data?.current ?? data?.Current ?? (data?.temperature !== undefined ? data : undefined);
  const hasCurrent = Boolean(
    currentCandidate && typeof currentCandidate === 'object' && !Array.isArray(currentCandidate),
  );
  const current = hasCurrent ? (currentCandidate as Record<string, unknown>) : undefined;

  const rawPack = typeof config?.character_pack === 'string' ? config.character_pack.trim() : '';
  const characterPack =
    rawPack && rawPack.toLowerCase() !== 'none'
      ? rawPack.replace(/[^a-zA-Z0-9_-]/g, '')
      : 'ghibli_school_day';
  const variantsPerState =
    typeof config?.variants_per_state === 'number' && config.variants_per_state >= 1
      ? Math.floor(config.variants_per_state)
      : 5;

  const isSchoolDay = config?.mode === 'school-day';

  const rawLoc =
    (typeof config?.name === 'string' && config.name) ||
    (typeof config?.location_name === 'string' && config.location_name) ||
    (typeof data?.location === 'string' && data.location) ||
    (typeof data?.Location === 'string' && data.Location) ||
    '';
  const locationName = rawLoc.trim();

  const showHeader = Boolean(locationName || hasCurrent);

  const conditionText = hasCurrent
    ? String(current?.condition_text ?? current?.ConditionText ?? '')
    : '';
  const iconName = hasCurrent
    ? String(current?.icon ?? current?.Icon ?? '')
    : 'weather-partly-cloudy';

  const rawTemp = current?.temperature ?? current?.Temperature ?? current?.temp ?? current?.Temp;
  const numTemp = typeof rawTemp === 'number' ? rawTemp : Number(rawTemp) || 0;
  const roundedTemp = Math.round(numTemp);

  const units = (current?.units ?? current?.Units) as Record<string, unknown> | undefined;
  const tempUnit = String(units?.temperature ?? units?.Temperature ?? '°');

  const daily = (data?.daily ?? data?.Daily) as unknown[] | undefined;
  const today =
    Array.isArray(daily) && daily.length > 0 && typeof daily[0] === 'object'
      ? (daily[0] as Record<string, unknown>)
      : undefined;
  const rawMax = today?.temp_max ?? today?.TempMax;
  const rawMin = today?.temp_min ?? today?.TempMin;
  const hasDaily = rawMax !== undefined && rawMin !== undefined;
  const maxTemp = hasDaily ? Math.round(Number(rawMax)) : 0;
  const minTemp = hasDaily ? Math.round(Number(rawMin)) : 0;

  const hourly = (data?.hourly ?? data?.Hourly) as unknown[] | undefined;
  const hasHourly = Array.isArray(hourly) && hourly.length > 0;

  const rawHumidity = current?.humidity ?? current?.Humidity;
  const humidity = rawHumidity !== undefined ? `${rawHumidity}%` : '--%';

  const rawWind = current?.wind_speed ?? current?.WindSpeed ?? current?.wind ?? current?.Wind;
  const windSpeed = Math.round(Number(rawWind) || 0);
  const windUnit = String(units?.wind_speed ?? units?.WindSpeed ?? 'mph');

  const precipProb = todayPrecipProb(data);
  const hourly = (data?.hourly ?? data?.Hourly) as unknown[] | undefined;

  const dateStr =
    (typeof config?.date === 'string' && config.date) ||
    (typeof entry?.timestamp === 'string' && entry.timestamp.slice(0, 10)) ||
    '2026-10-31';

  const schoolDayThresholds: SchoolDayThresholds = {
    rainThresholdPct:
      typeof config?.rain_threshold_pct === 'number'
        ? config.rain_threshold_pct
        : DEFAULT_SCHOOL_DAY_THRESHOLDS.rainThresholdPct,
    coldMaxF:
      typeof config?.cold_max_f === 'number'
        ? config.cold_max_f
        : DEFAULT_SCHOOL_DAY_THRESHOLDS.coldMaxF,
    coolMaxF:
      typeof config?.cool_max_f === 'number'
        ? config.cool_max_f
        : DEFAULT_SCHOOL_DAY_THRESHOLDS.coolMaxF,
    warmMaxF:
      typeof config?.warm_max_f === 'number'
        ? config.warm_max_f
        : DEFAULT_SCHOOL_DAY_THRESHOLDS.warmMaxF,
  };

  const schoolDaySlots = isSchoolDay
    ? DAYPART_SLOTS.map((slot) => {
        const info = resolveSlotWeather(slot, data, current, daily, hourly);
        const state = resolveClothingConditionState(
          info.temp,
          info.precipProb,
          info.icon,
          slot,
          schoolDayThresholds,
        );
        const variantIndex = selectDailyVariantIndex(dateStr, slot, variantsPerState);
        const spriteUrl = resolveCharacterPackSpriteUrl(characterPack, state, variantIndex);
        return {
          slot,
          temp: info.temp,
          state,
          spriteUrl,
        };
      })
    : [];

  return (
    <div className="widget-weather">
      {isError && (
        <div className="widget-error-banner">
          <span className="error-icon">⚠</span>
          <span>Widget Error</span>
        </div>
      )}

      {showHeader &&
        renderCurrentHeader({
          locationName,
          hasCurrent,
          conditionText,
          iconName,
          roundedTemp,
          tempUnit,
          hasDaily,
          maxTemp,
          minTemp,
        })}

      {hasCurrent ? (
        <>
          {isSchoolDay && (
            <div className="weather-school-day-strip">
              {schoolDaySlots.map((item) => (
                <div
                  key={item.slot}
                  className={`weather-school-day-slot weather-school-day-slot-${item.slot}`}
                  data-slot={item.slot}
                >
                  <span className="weather-school-day-name weather-school-day-slot-name">
                    {item.slot}
                  </span>
                  <span className="weather-school-day-temp">{item.temp}°</span>
                  <span className="weather-school-day-state weather-school-day-condition">
                    {item.state}
                  </span>
                  <img
                    className="weather-school-day-sprite"
                    src={item.spriteUrl}
                    alt={item.state}
                  />
                </div>
              ))}
            </div>
          )}

          <div className="weather-container weather-hero weather-Hero">
            {hasHourly ? (
              <div className="weather-hourly-timeline">
                <WeatherHourlyGraph hourly={hourly} widgetId={id} />
              </div>
            ) : (
              <div className="weather-empty">
                <span className="empty-icon">
                  <svg
                    width="24"
                    height="24"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <circle cx="12" cy="12" r="4" />
                    <path d="M12 2v2" />
                    <path d="M12 20v2" />
                    <path d="m4.93 4.93 1.41 1.41" />
                    <path d="m17.66 17.66 1.41 1.41" />
                    <path d="M2 12h2" />
                    <path d="M20 12h2" />
                    <path d="m6.34 17.66-1.41 1.41" />
                    <path d="m19.07 4.93-1.41 1.41" />
                  </svg>
                </span>
                <span className="empty-title">No hourly forecast available</span>
              </div>
            )}
          </div>

          <div className="weather-micro-stats">
            <span
              className="weather-meta-stat weather-meta-humidity"
              title={`Humidity: ${humidity}`}
            >
              <svg
                className="weather-meta-icon"
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M12 2.69l5.66 5.66a8 8 0 1 1-11.31 0z" />
              </svg>
              <span>{humidity}</span>
            </span>
            <span
              className="weather-meta-stat weather-meta-wind"
              title={`Wind: ${windSpeed} ${windUnit}`}
            >
              <svg
                className="weather-meta-icon"
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M17.7 7.7a2.5 2.5 0 1 1 1.8 4.3H2" />
                <path d="M9.6 4.6A2 2 0 1 1 11 8H2" />
                <path d="M12.6 19.4A2 2 0 1 0 14 16H2" />
              </svg>
              <span>
                {windSpeed} {windUnit}
              </span>
            </span>
            <span
              className="weather-meta-stat weather-meta-precip"
              title={`Precipitation: ${precipProb}%`}
            >
              <svg
                className="weather-meta-icon"
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M22 12a10.06 10.06 0 0 0-20 0Z" />
                <path d="M12 12v8a2 2 0 0 0 4 0" />
                <path d="M12 2v1" />
              </svg>
              <span>{precipProb}%</span>
            </span>
          </div>
        </>
      ) : (
        <div className="weather-loading">
          <span>Loading weather forecast...</span>
        </div>
      )}
    </div>
  );
}
