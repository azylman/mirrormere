import { useHeader } from '../store/hooks';

export const WEATHER_ICON_PATHS: Record<string, string> = {
  'weather-sunny':
    '<circle cx="12" cy="12" r="5" fill="#f59e0b" stroke="#f59e0b" stroke-width="1"/><g stroke="#f59e0b" stroke-width="2" stroke-linecap="round"><line x1="12" y1="1.5" x2="12" y2="3.5"/><line x1="12" y1="20.5" x2="12" y2="22.5"/><line x1="4.5" y1="4.5" x2="6" y2="6"/><line x1="18" y1="18" x2="19.5" y2="19.5"/><line x1="1.5" y1="12" x2="3.5" y2="12"/><line x1="20.5" y1="12" x2="22.5" y2="12"/><line x1="4.5" y1="19.5" x2="6" y2="18"/><line x1="18" y1="6" x2="19.5" y2="4.5"/></g>',
  'weather-clear-night':
    '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
  'weather-partly-cloudy':
    '<circle cx="16" cy="9" r="4" fill="#f59e0b"/><g stroke="#f59e0b" stroke-width="1.8" stroke-linecap="round"><line x1="16" y1="2" x2="16" y2="3.5"/><line x1="21.5" y1="6" x2="20" y2="7"/><line x1="21.5" y1="12" x2="20" y2="11"/><line x1="11.5" y1="4" x2="12.5" y2="5"/></g><path d="M15.5 19H7a4.5 4.5 0 0 1-.8-8.9A5.5 5.5 0 0 1 16.8 11 3.5 3.5 0 0 1 15.5 19z" fill="#ffffff" stroke="#94a3b8" stroke-width="1.6" stroke-linejoin="round"/>',
  'weather-night-partly-cloudy':
    '<path d="M19 10.5A6 6 0 0 1 12.5 4a5 5 0 0 0 6.5 6.5z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.5" stroke-linejoin="round"/><path d="M15.5 19H7a4.5 4.5 0 0 1-.8-8.9A5.5 5.5 0 0 1 16.8 11 3.5 3.5 0 0 1 15.5 19z" fill="#ffffff" stroke="#94a3b8" stroke-width="1.6" stroke-linejoin="round"/>',
  'weather-cloudy':
    '<path d="M17.5 19H7a5 5 0 0 1-1-9.9A6 6 0 0 1 17.8 11 4 4 0 0 1 17.5 19z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/>',
  'weather-fog':
    '<g stroke="#94a3b8" stroke-width="2" stroke-linecap="round"><line x1="4" y1="10" x2="20" y2="10"/><line x1="3" y1="14" x2="21" y2="14"/><line x1="5" y1="18" x2="19" y2="18"/><line x1="7" y1="6" x2="17" y2="6"/></g>',
  'weather-rainy':
    '<path d="M17.5 15H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 7 3.5 3.5 0 0 1 17.5 15z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g stroke="#38bdf8" stroke-width="2" stroke-linecap="round"><line x1="8" y1="18" x2="7" y2="21"/><line x1="12" y1="18" x2="11" y2="21"/><line x1="16" y1="18" x2="15" y2="21"/></g>',
  'weather-pouring':
    '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><g stroke="#0284c7" stroke-width="2" stroke-linecap="round"><line x1="6" y1="17" x2="4" y2="21"/><line x1="10" y1="17" x2="8" y2="21"/><line x1="14" y1="17" x2="12" y2="21"/><line x1="18" y1="17" x2="16" y2="21"/></g>',
  'weather-snowy':
    '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g fill="#93c5fd"><circle cx="8" cy="18" r="1.3"/><circle cx="12" cy="18" r="1.3"/><circle cx="16" cy="18" r="1.3"/><circle cx="10" cy="21" r="1.3"/><circle cx="14" cy="21" r="1.3"/></g>',
  'weather-snowy-rainy':
    '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g fill="#93c5fd"><circle cx="8" cy="18" r="1.3"/><circle cx="14" cy="18" r="1.3"/></g><g stroke="#38bdf8" stroke-width="2" stroke-linecap="round"><line x1="11" y1="18" x2="10" y2="21"/></g>',
  'weather-lightning':
    '<path d="M17.5 13H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 5 3.5 3.5 0 0 1 17.5 13z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><polygon points="12 13 9 18 13 18 11 23 16 16 12 16 14 13" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linejoin="round"/>',
  'weather-lightning-rainy':
    '<path d="M17.5 13H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 5 3.5 3.5 0 0 1 17.5 13z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><polygon points="12 13 9 18 13 18 11 23 16 16 12 16 14 13" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linejoin="round"/>',
};

export const WEATHER_DESC_MAP: Record<string, string> = {
  'weather-sunny': 'Sunny & Mild',
  'weather-clear-night': 'Clear',
  'weather-partly-cloudy': 'Partly Cloudy',
  'weather-night-partly-cloudy': 'Partly Cloudy',
  'weather-cloudy': 'Overcast',
  'weather-fog': 'Misty & Foggy',
  'weather-rainy': 'Light Rain',
  'weather-pouring': 'Heavy Rain',
  'weather-snowy': 'Snow Showers',
  'weather-snowy-rainy': 'Rain & Snow',
  'weather-lightning': 'Thunderstorm',
  'weather-lightning-rainy': 'Severe Storm',
};

export function WeatherPill() {
  const header = useHeader();
  const weather = header?.weather;

  const tempText =
    weather && weather.temperature !== undefined
      ? `${Math.round(weather.temperature)}°${(weather.units || '°').replace('°', '')}`
      : '--°';

  const locationText = weather?.location || 'Oakland';

  const conditionToken = weather?.icon || weather?.condition || '';
  const customDesc = (weather as { description?: string } | null | undefined)?.description;

  let descText = 'Sunny & Mild';
  if (weather) {
    if (customDesc) {
      descText = customDesc;
    } else if (WEATHER_DESC_MAP[conditionToken]) {
      descText = WEATHER_DESC_MAP[conditionToken];
    } else if (weather.condition && WEATHER_DESC_MAP[weather.condition]) {
      descText = WEATHER_DESC_MAP[weather.condition];
    } else if (weather.condition && weather.condition !== conditionToken) {
      descText = weather.condition;
    } else {
      const isNight =
        conditionToken.includes('night') ||
        (weather.condition ? weather.condition.toLowerCase().includes('night') : false);
      descText = isNight ? 'Clear' : (weather.condition || 'Clear Sky');
    }
  }

  const iconKey =
    conditionToken && WEATHER_ICON_PATHS[conditionToken]
      ? conditionToken
      : conditionToken.includes('night')
        ? 'weather-clear-night'
        : 'weather-partly-cloudy';

  const safeToken = iconKey.replace(/[^a-z0-9_-]/gi, '');
  const iconPath = WEATHER_ICON_PATHS[iconKey] || WEATHER_ICON_PATHS['weather-partly-cloudy'];

  return (
    <div className="header-weather" id="header-weather">
      <div className="pill-badge weather-pill" id="weather-pill">
        <span className="weather-condition" id="weather-condition" title={conditionToken || undefined}>
          <svg
            width="26"
            height="26"
            viewBox="0 0 24 24"
            fill="none"
            className={`mm-weather-icon mm-icon-${safeToken}`}
            dangerouslySetInnerHTML={{ __html: iconPath }}
          />
        </span>
        <div className="weather-details" id="weather-details">
          <div className="weather-primary-row">
            <span className="weather-temp" id="weather-temp">
              {tempText}
            </span>
            <span className="weather-location" id="weather-location">
              {locationText}
            </span>
          </div>
          <span className="weather-desc" id="weather-desc">
            {descText}
          </span>
        </div>
      </div>
    </div>
  );
}
