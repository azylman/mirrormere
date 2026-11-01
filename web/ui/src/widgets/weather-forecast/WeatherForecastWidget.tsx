import { memo } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import { todayPrecipProb, weatherHourlyGraphSvg, weatherIconSvg } from '../format';

/**
 * Native React widget component for weather forecasts.
 * Ports widgets/weather-forecast/views/widget.html.
 */
export const WeatherForecastWidget = memo(function WeatherForecastWidget({
  id,
}: WidgetComponentProps) {
  const entry = useWidget(id);
  const config = entry?.meta?.config as Record<string, unknown> | undefined;
  const data = entry?.data as Record<string, unknown> | undefined;
  const isError = entry?.state === 'error';

  const currentCandidate = data?.current ?? data?.Current;
  const hasCurrent = Boolean(
    currentCandidate && typeof currentCandidate === 'object' && !Array.isArray(currentCandidate),
  );
  const current = hasCurrent ? (currentCandidate as Record<string, unknown>) : undefined;

  const locationName =
    (typeof config?.name === 'string' && config.name.trim() !== '' && config.name.trim()) ||
    (typeof config?.location_name === 'string' &&
      config.location_name.trim() !== '' &&
      config.location_name.trim()) ||
    (typeof data?.location === 'string' && data.location.trim() !== '' && data.location.trim()) ||
    (typeof data?.Location === 'string' && data.Location.trim() !== '' && data.Location.trim()) ||
    '';

  const showHeader = Boolean(locationName || hasCurrent);

  const conditionText = hasCurrent
    ? String(current?.condition_text ?? current?.ConditionText ?? '')
    : '';
  const iconName = hasCurrent
    ? String(current?.icon ?? current?.Icon ?? '')
    : 'weather-partly-cloudy';

  const rawTemp = current?.temperature ?? current?.Temperature ?? current?.temp ?? current?.Temp;
  const numTemp = typeof rawTemp === 'number' ? rawTemp : Number(rawTemp) || 0;
  const roundedTemp = Math.sign(numTemp) * Math.round(Math.abs(numTemp)) || 0;

  const units = (current?.units ?? current?.Units) as Record<string, unknown> | undefined;
  const tempUnit = String(units?.temperature ?? units?.Temperature ?? '°');

  const daily = (data?.daily ?? data?.Daily) as unknown[] | undefined;
  const today =
    Array.isArray(daily) && daily.length > 0 && typeof daily[0] === 'object'
      ? (daily[0] as Record<string, unknown>)
      : undefined;
  const rawMax = today?.temp_max ?? today?.TempMax ?? today?.max_temp ?? today?.MaxTemp;
  const rawMin = today?.temp_min ?? today?.TempMin ?? today?.min_temp ?? today?.MinTemp;
  const hasDaily = rawMax !== undefined && rawMin !== undefined;
  const maxTemp = hasDaily
    ? Math.sign(Number(rawMax) || 0) * Math.round(Math.abs(Number(rawMax) || 0)) || 0
    : 0;
  const minTemp = hasDaily
    ? Math.sign(Number(rawMin) || 0) * Math.round(Math.abs(Number(rawMin) || 0)) || 0
    : 0;

  const hourly = (data?.hourly ?? data?.Hourly) as unknown[] | undefined;
  const hasHourly = Array.isArray(hourly) && hourly.length > 0;

  const rawHumidity = current?.humidity ?? current?.Humidity;
  const humidity = rawHumidity !== undefined ? `${rawHumidity}%` : '--%';

  const rawWind = current?.wind_speed ?? current?.WindSpeed ?? current?.wind ?? current?.Wind;
  const windSpeed =
    Math.sign(Number(rawWind) || 0) * Math.round(Math.abs(Number(rawWind) || 0)) || 0;
  const windUnit = String(units?.wind_speed ?? units?.WindSpeed ?? 'mph');

  const precipProb = todayPrecipProb(data);

  return (
    <div className="widget-weather">
      {isError && (
        <div className="widget-error-banner">
          <span className="error-icon">⚠</span>
          <span>Widget Error</span>
        </div>
      )}

      {showHeader && (
        <div className="weather-header">
          <div className="weather-header-left">
            <div
              className="weather-icon-badge"
              title={conditionText}
              dangerouslySetInnerHTML={{ __html: weatherIconSvg(iconName, 20) }}
            />
            <div className="weather-header-titles">
              {locationName ? (
                <h2 className="weather-location-name">{locationName}</h2>
              ) : (
                <h2 className="weather-header-title">Weather</h2>
              )}
              <span className="weather-condition-text">{conditionText || 'Forecast'}</span>
            </div>
          </div>

          {hasCurrent && (
            <div className="weather-header-right">
              <div
                className="weather-temp-main weather-current-main bubbly-font"
                title={conditionText}
              >
                {roundedTemp}
                {tempUnit}
              </div>
              {hasDaily && (
                <div className="weather-temp-range">
                  H: {maxTemp}° • L: {minTemp}°
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {hasCurrent ? (
        <>
          <div className="weather-container weather-hero weather-Hero">
            {hasHourly ? (
              <div
                className="weather-hourly-timeline"
                dangerouslySetInnerHTML={{ __html: weatherHourlyGraphSvg(hourly, id) }}
              />
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
});
