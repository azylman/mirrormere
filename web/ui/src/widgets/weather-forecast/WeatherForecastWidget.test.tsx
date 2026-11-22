import { render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import * as formatModule from '../format';
import { useDisplayStore } from '../../store';
import { renderWidget, seedWidget, widgetMeta } from '../../test-utils/widget';
import { WeatherForecastWidget } from './WeatherForecastWidget';
import { WeatherIcon, WeatherIconGlyph } from './WeatherIcon';
import { WeatherHourlyGraph } from './WeatherHourlyGraph';
import {
  addOneCalendarDay,
  formatSlotForecastSummary,
  resolveClothingOutfitLabel,
  resolveIconConditionLabel,
  resolveSchoolDayTargetDate,
} from './schoolDayPack';

const WF = 'weather-forecast';
const SCHOOL_DAY = { mode: 'school-day' };

/** Like seedWidget, but with a store timestamp other than the helper's fixed noon one (school-day date rollover). */
function seedWeatherAt(id: string, config: Record<string, unknown>, data: unknown, timestamp: string): void {
  useDisplayStore.setState({ widgets: { [id]: { meta: widgetMeta(WF, config), data, state: 'ready', timestamp } } });
}

/** Seeds a weather widget and renders it (the common arrange step). */
function renderWeather(id: string, config: Record<string, unknown>, data: unknown, state?: string) {
  seedWidget(id, widgetMeta(WF, config), data, state);
  return renderWidget(WeatherForecastWidget, id, WF);
}

describe('WeatherForecastWidget', () => {

  it('renders unnamed loading state without a header when data is {}', () => {
    const { container } = renderWeather('weather-unnamed', {}, {});
    const root = container.querySelector('.widget-weather');
    expect(root).not.toBeNull();
    expect(root?.querySelector('.weather-header')).toBeNull();
    expect(root?.querySelector('.weather-loading')?.textContent).toContain('Loading weather forecast...');
  });

  it('renders named loading state with header when config.location_name is set but data is unhydrated', () => {
    const { container } = renderWeather('weather-named', { location_name: 'Hilltop' }, {});
    const root = container.querySelector('.widget-weather');
    expect(root?.querySelector('.weather-header')).not.toBeNull();
    expect(root?.querySelector('.weather-location-name')?.textContent).toBe('Hilltop');
    expect(root?.querySelector('.weather-loading')?.textContent).toContain('Loading weather forecast...');
  });

  it('renders header using config.name', () => {
    const { container } = renderWeather('weather-by-name', { name: 'Home Base' }, { current: { temperature: 72 } });
    expect(container.querySelector('.weather-location-name')?.textContent).toBe('Home Base');
  });

  it('renders header with default Weather title when location is omitted', () => {
    const { container } = renderWeather('weather-no-loc', {}, { current: { temperature: 70 } });
    expect(container.querySelector('.weather-header-title')?.textContent).toBe('Weather');
  });

  it('renders negative temperatures accurately', () => {
    const { container } = renderWeather('weather-freezing', {}, {
      current: { temperature: -4.2 }, daily: [{ temp_max: 0, temp_min: -8 }],
    });
    expect(container.querySelector('.weather-temp-main')?.textContent).toBe('-4°');
    expect(container.querySelector('.weather-temp-range')?.textContent).toBe('H: 0° • L: -8°');
  });

  it('renders full current conditions and hourly SVG graph with snake_case payload', () => {
    const { container } = renderWeather('weather-snake', {}, {
      location: 'Lakeside',
      current: {
        temperature: 68.4, condition_text: 'Sunny', icon: 'weather-sunny', humidity: 55, wind_speed: 12.3,
        units: { temperature: '°F', wind_speed: 'mph' },
      },
      daily: [{ temp_max: 74.2, temp_min: 52.8, precip_prob_max: 10 }],
      hourly: [
        { time: '2026-10-31T12:00:00Z', temp: 68, precip_prob: 0, icon: 'weather-sunny' },
        { time: '2026-10-31T13:00:00Z', temp: 70, precip_prob: 5, icon: 'weather-sunny' },
        { time: '2026-10-31T14:00:00Z', temp: 72, precip_prob: 10, icon: 'weather-partly-cloudy' },
      ],
    });
    const root = container.querySelector('.widget-weather');
    expect(root).not.toBeNull();

    // Header
    expect(root?.querySelector('.weather-location-name')?.textContent).toBe('Lakeside');
    expect(root?.querySelector('.weather-condition-text')?.textContent).toBe('Sunny');
    expect(root?.querySelector('.weather-icon-badge svg')).not.toBeNull();

    // Main temp and high/low
    expect(root?.querySelector('.weather-temp-main')?.textContent).toBe('68°F');
    expect(root?.querySelector('.weather-temp-range')?.textContent).toBe('H: 74° • L: 53°');

    // Hourly graph SVG inside timeline
    const timeline = root?.querySelector('.weather-hourly-timeline');
    expect(timeline).not.toBeNull();
    const svg = timeline?.querySelector('svg.weather-hourly-graph');
    expect(svg).not.toBeNull();

    // Micro stats
    expect(root?.querySelector('.weather-meta-humidity')?.textContent).toContain('55%');
    expect(root?.querySelector('.weather-meta-wind')?.textContent).toContain('12 mph');
    expect(root?.querySelector('.weather-meta-precip')?.textContent).toContain('10%');
  });

  it('renders full current conditions and hourly SVG graph with PascalCase payload', () => {
    const { container } = renderWeather('weather-pascal', {}, {
      Location: 'Seattle',
      Current: {
        Temperature: 54.6, ConditionText: 'Rainy', Icon: 'weather-rainy', Humidity: 88, WindSpeed: 8.7,
        Units: { Temperature: '°', WindSpeed: 'knots' },
      },
      Daily: [{ TempMax: 58.1, TempMin: 49.3, PrecipProbMax: 80 }],
      Hourly: [
        { Time: '2026-10-31T12:00:00Z', Temp: 54, PrecipProb: 80, Icon: 'weather-rainy' },
        { Time: '2026-10-31T13:00:00Z', Temp: 55, PrecipProb: 75, Icon: 'weather-rainy' },
      ],
    });
    const root = container.querySelector('.widget-weather');
    expect(root).not.toBeNull();

    expect(root?.querySelector('.weather-location-name')?.textContent).toBe('Seattle');
    expect(root?.querySelector('.weather-condition-text')?.textContent).toBe('Rainy');
    expect(root?.querySelector('.weather-temp-main')?.textContent).toBe('55°');
    expect(root?.querySelector('.weather-temp-range')?.textContent).toBe('H: 58° • L: 49°');

    expect(root?.querySelector('.weather-meta-humidity')?.textContent).toContain('88%');
    expect(root?.querySelector('.weather-meta-wind')?.textContent).toContain('9 knots');
    expect(root?.querySelector('.weather-meta-precip')?.textContent).toContain('80%');
  });

  it('renders empty forecast placeholder when current exists but hourly is empty', () => {
    const { container } = renderWeather('weather-no-hourly', {}, {
      current: { temperature: 70, condition_text: 'Clear', icon: 'weather-sunny' }, hourly: [],
    });
    const empty = container.querySelector('.weather-empty');
    expect(empty).not.toBeNull();
    expect(empty?.textContent).toContain('No hourly forecast available');
  });

  it('renders widget-error-banner when state is error', () => {
    // Raw store entry on purpose: an error entry with no meta at all.
    useDisplayStore.setState({
      widgets: { 'weather-err': { state: 'error', data: {}, timestamp: '' } },
    });
    const { container } = render(<WeatherForecastWidget id="weather-err" type="weather-forecast" />);
    const banner = container.querySelector('.widget-error-banner');
    expect(banner).not.toBeNull();
    expect(banner?.textContent).toContain('Widget Error');
  });

  it('renders school-day mode with character pack strip and sprite images', () => {
    const { container } = renderWeather('weather-school', {
      mode: 'school-day', character_pack: 'ghibli_school_day', variants_per_state: 5,
      rain_threshold_pct: 35, cold_max_f: 49, cool_max_f: 64, warm_max_f: 77,
    }, {
      location: 'Springfield',
      current: { temperature: 62, condition_text: 'Partly Cloudy', icon: 'weather-partly-cloudy', humidity: 45, wind_speed: 8 },
      daily: [{ temp_max: 75, temp_min: 48, precip_prob_max: 20 }],
      hourly: [
        { time: '2026-10-31T08:00:00Z', temp: 48, precip_prob: 10, icon: 'weather-sunny' },
        { time: '2026-10-31T14:00:00Z', temp: 75, precip_prob: 15, icon: 'weather-partly-cloudy' },
        { time: '2026-10-31T18:00:00Z', temp: 60, precip_prob: 10, icon: 'weather-cloudy' },
      ],
    });

    const strip = container.querySelector('.weather-school-day-strip');
    expect(strip).not.toBeNull();

    const slots = container.querySelectorAll('.weather-school-day-slot');
    expect(slots.length).toBe(3);

    const sprites = container.querySelectorAll('img.weather-school-day-sprite');
    expect(sprites.length).toBe(3);

    // Morning slot: temp 48 <= 49 -> cold_puffer
    const morningSlot = container.querySelector('.weather-school-day-slot-morning');
    expect(morningSlot?.textContent).toContain('morning');
    expect(morningSlot?.textContent).toContain('48°');
    expect(morningSlot?.textContent).toContain('cold_puffer');
    const morningImg = morningSlot?.querySelector('img.weather-school-day-sprite');
    expect(morningImg?.getAttribute('src')).toMatch(
      /^\/static\/ui\/assets\/weather-packs\/ghibli_school_day\/cold_puffer_v\d+\.jpg$/,
    );

    // Afternoon slot: temp 75 <= 77 -> warm_pants
    const afternoonSlot = container.querySelector('.weather-school-day-slot-afternoon');
    expect(afternoonSlot?.textContent).toContain('afternoon');
    expect(afternoonSlot?.textContent).toContain('75°');
    expect(afternoonSlot?.textContent).toContain('warm_pants');
    const afternoonImg = afternoonSlot?.querySelector('img.weather-school-day-sprite');
    expect(afternoonImg?.getAttribute('src')).toMatch(
      /^\/static\/ui\/assets\/weather-packs\/ghibli_school_day\/warm_pants_v\d+\.jpg$/,
    );

    // Evening slot: temp 60 <= 64 -> cool_evening
    const eveningSlot = container.querySelector('.weather-school-day-slot-evening');
    expect(eveningSlot?.textContent).toContain('evening');
    expect(eveningSlot?.textContent).toContain('60°');
    expect(eveningSlot?.textContent).toContain('cool_evening');
    const eveningImg = eveningSlot?.querySelector('img.weather-school-day-sprite');
    expect(eveningImg?.getAttribute('src')).toMatch(
      /^\/static\/ui\/assets\/weather-packs\/ghibli_school_day\/cool_evening_v\d+\.jpg$/,
    );
  });

  it('renders school-day mode with default character pack and fallback when hourly is missing', () => {
    const { container } = renderWeather('weather-school-default', {
      mode: 'school-day', character_pack: 'none', variants_per_state: 0,
    }, {
      current: { temperature: 82, condition_text: 'Sunny', icon: 'weather-sunny' },
      daily: [{ temp_max: 85, temp_min: 52, date: '2026-10-31' }],
    });

    const strip = container.querySelector('.weather-school-day-strip');
    expect(strip).not.toBeNull();
    // Character pack defaults to ghibli_school_day when none/empty
    const sprites = container.querySelectorAll('img.weather-school-day-sprite');
    expect(sprites.length).toBe(3);
    for (const sprite of sprites) {
      expect(sprite.getAttribute('src')).toContain('/ghibli_school_day/');
    }

    // Morning uses minTemp (52 > 49 -> morning_mild)
    const morning = container.querySelector('.weather-school-day-slot-morning');
    expect(morning?.textContent).toContain('52°');
    expect(morning?.textContent).toContain('morning_mild');

    // Afternoon uses maxTemp (85 > 77 -> hot_dress)
    const afternoon = container.querySelector('.weather-school-day-slot-afternoon');
    expect(afternoon?.textContent).toContain('85°');
    expect(afternoon?.textContent).toContain('hot_dress');
  });

  it('renders afternoon and evening in school-day mode with daily temp range fallback', () => {
    const { container } = renderWeather('weather-school-daily', { mode: 'school-day', date: '2026-10-31' }, {
      current: { temperature: 70 }, daily: [{ temp_max: 80, temp_min: 50 }],
    });
    const evening = container.querySelector('.weather-school-day-slot-evening');
    // (50 + 80) / 2 = 65 -> warm_pants (65 <= 77)
    expect(evening?.textContent).toContain('65°');
    expect(evening?.textContent).toContain('warm_pants');
  });

  it('renders school-day mode with direct slot overrides and rainy conditions', () => {
    const { container } = renderWeather('weather-school-overrides', {
      mode: 'school-day', character_pack: 'custom-pack', variants_per_state: 3, date: '2026-10-31',
    }, {
      current: { temperature: 55, icon: 'weather-rainy' },
      slots: {
        morning: { temp: 50, precip_prob: 60, icon: 'weather-rainy' },
        afternoon: { temp: 55, precip_prob: 80, icon: 'weather-pouring' },
        evening: { temp: 45, precip_prob: 10, icon: 'weather-cloudy' },
      },
    });

    const morning = container.querySelector('.weather-school-day-slot-morning');
    expect(morning?.textContent).toContain('morning_rain');
    expect(morning?.querySelector('img')?.getAttribute('src')).toContain('/custom-pack/');

    const afternoon = container.querySelector('.weather-school-day-slot-afternoon');
    expect(afternoon?.textContent).toContain('rainy_boots');

    const evening = container.querySelector('.weather-school-day-slot-evening');
    expect(evening?.textContent).toContain('cold_puffer');
  });

  it('renders school-day mode fallback with root temperature and unparsed hourly times', () => {
    const { container } = renderWeather('weather-school-unparsed', SCHOOL_DAY, {
      temperature: 68,
      hourly: [
        { time: 'invalid-time', temp: 65, precip_prob: 0, icon: 'weather-sunny' },
        { time: 'another-invalid', temp: 72, precip_prob: 5, icon: 'weather-sunny' },
        { time: 'third-invalid', temp: 60, precip_prob: 0, icon: 'weather-sunny' },
      ],
    });

    const strip = container.querySelector('.weather-school-day-strip');
    expect(strip).not.toBeNull();
    const morning = container.querySelector('.weather-school-day-slot-morning');
    expect(morning?.textContent).toContain('65°');
  });

  describe('school-day slot weather from payload variants', () => {
    // Each row renders school-day mode and checks the temperature each daypart slot shows.
    const tests: {
      name: string;
      data: Record<string, unknown>;
      timestamp?: string;
      wantText: [slot: 'morning' | 'afternoon' | 'evening', text: string][];
    }[] = [
      {
        name: 'reads slot temperature/precip aliases and PascalCase Hourly, skipping null hourly entries',
        data: {
          Current: { Temperature: 65, Icon: 'weather-sunny' },
          slots: { morning: { temperature: 45, precip: 10 } },
          Hourly: [
            null,
            { Time: '2026-10-31T14:00:00Z', Temp: 75, PrecipProb: 10, Icon: 'weather-sunny' },
            { Time: '2026-10-31T18:00:00Z', Temp: 60, PrecipProb: 5, Icon: 'weather-cloudy' },
          ],
        },
        wantText: [['morning', '45°'], ['afternoon', '75°']],
      },
      {
        name: 'falls back to PascalCase Daily TempMin/TempMax when hourly is missing',
        data: { Current: { Temperature: 65 }, Daily: [{ TempMax: 78, TempMin: 48 }] },
        wantText: [['morning', '48°'], ['afternoon', '78°']],
      },
      {
        name: 'picks every daypart from PascalCase Hourly entries',
        data: {
          Current: { Temperature: 60 },
          Hourly: [
            { Time: '2026-10-31T08:00:00Z', Temp: 52, PrecipProb: 5, Icon: 'weather-sunny' },
            { Time: '2026-10-31T14:00:00Z', Temp: 74, PrecipProb: 15, Icon: 'weather-partly-cloudy' },
            { Time: '2026-10-31T18:00:00Z', Temp: 58, PrecipProb: 10, Icon: 'weather-clear-night' },
          ],
        },
        wantText: [['morning', '52°'], ['afternoon', '74°'], ['evening', '58°']],
      },
      {
        name: 'uses the root temperature for every slot with empty daily, no current and an unparseable timestamp',
        data: { temperature: 62, daily: [] },
        timestamp: 'short',
        wantText: [['morning', '62°'], ['afternoon', '62°'], ['evening', '62°']],
      },
    ];

    for (const tc of tests) {
      it(tc.name, () => {
        if (tc.timestamp === undefined) seedWidget('weather-variant', widgetMeta(WF, SCHOOL_DAY), tc.data);
        else seedWeatherAt('weather-variant', SCHOOL_DAY, tc.data, tc.timestamp);
        const { container } = renderWidget(WeatherForecastWidget, 'weather-variant', WF);
        expect(container.querySelectorAll('.weather-school-day-slot').length).toBe(3);
        for (const [slot, text] of tc.wantText) {
          expect(container.querySelector(`.weather-school-day-slot-${slot}`)?.textContent).toContain(text);
        }
      });
    }
  });

  it('renders safely when widget id is not found in store', () => {
    const { container } = render(
      <WeatherForecastWidget id="non-existent-widget" type="weather-forecast" />,
    );
    expect(container.querySelector('.weather-loading')?.textContent).toContain(
      'Loading weather forecast...',
    );
    expect(container.querySelector('.weather-header')).toBeNull();
  });

  it('renders error banner alongside current weather data when state is error', () => {
    const { container } = renderWeather('weather-err-with-data', { name: 'Faulty Station' }, {
      current: { temperature: 65, condition_text: 'Degraded' },
    }, 'error');
    expect(container.querySelector('.widget-error-banner')).not.toBeNull();
    expect(container.querySelector('.weather-location-name')?.textContent).toBe('Faulty Station');
    expect(container.querySelector('.weather-temp-main')?.textContent).toContain('65°');
  });

  it('extracts wind from wind and Wind properties and handles hourly with no temp or precip', () => {
    seedWeatherAt('weather-wind-alt', SCHOOL_DAY, {
      current: { temperature: 70, wind: 14 },
      hourly: [{ time: '2026-10-31T08:00:00Z' }, { time: '2026-10-31T14:00:00Z' }, { time: '2026-10-31T18:00:00Z' }],
    }, '2026-10');
    const { container, rerender } = renderWidget(WeatherForecastWidget, 'weather-wind-alt', WF);
    expect(container.querySelector('.weather-meta-wind')?.textContent).toContain('14 mph');

    // Also test PascalCase Wind and empty slots
    seedWeatherAt('weather-wind-alt', SCHOOL_DAY, {
      current: { temperature: 70, Wind: 18 },
      slots: { morning: {}, afternoon: {}, evening: {} },
    }, '');

    rerender(<WeatherForecastWidget id="weather-wind-alt" type="weather-forecast" />);
    expect(container.querySelector('.weather-meta-wind')?.textContent).toContain('18 mph');
    expect(container.querySelector('.weather-school-day-slot-morning')?.textContent).toContain('0°');
  });

});


describe('WeatherIcon component', () => {

  const tokens = [
    'weather-sunny',
    'weather-clear-night',
    'weather-partly-cloudy',
    'weather-night-partly-cloudy',
    'weather-cloudy',
    'weather-fog',
    'weather-rainy',
    'weather-pouring',
    'weather-snowy',
    'weather-snowy-rainy',
    'weather-lightning',
    'weather-lightning-rainy',
  ];

  for (const token of tokens) {
    it(`renders declarative SVG for token: ${token}`, () => {
      const { container } = render(<WeatherIcon name={token} size={24} />);
      const svg = container.querySelector('svg');
      expect(svg).not.toBeNull();
      expect(svg?.classList.contains(`mm-icon-${token}`)).toBe(true);
      expect(svg?.getAttribute('width')).toBe('24');
      expect(svg?.getAttribute('height')).toBe('24');
      expect(svg?.children.length).toBeGreaterThan(0);
    });
  }

  it('clamps size within [12, 64] and defaults to 20', () => {
    const { container: defaultContainer } = render(<WeatherIcon name="weather-sunny" />);
    expect(defaultContainer.querySelector('svg')?.getAttribute('width')).toBe('20');

    const { container: smallContainer } = render(<WeatherIcon name="weather-sunny" size={8} />);
    expect(smallContainer.querySelector('svg')?.getAttribute('width')).toBe('12');

    const { container: largeContainer } = render(<WeatherIcon name="weather-sunny" size={120} />);
    expect(largeContainer.querySelector('svg')?.getAttribute('width')).toBe('64');
  });

  it('falls back to weather-cloudy for unknown or null tokens and sanitizes token', () => {
    const { container: nullContainer } = render(<WeatherIcon name={null} />);
    expect(nullContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(
      true,
    );

    const { container: emptyContainer } = render(<WeatherIcon name="" />);
    expect(emptyContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(
      true,
    );

    const { container: spacesContainer } = render(<WeatherIcon name="   " size={NaN} />);
    expect(spacesContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(
      true,
    );
    expect(spacesContainer.querySelector('svg')?.getAttribute('width')).toBe('20');

    const { container: negContainer } = render(<WeatherIcon name="weather-sunny" size={-5} />);
    expect(negContainer.querySelector('svg')?.getAttribute('width')).toBe('20');

    const { container: zeroContainer } = render(<WeatherIcon name="weather-sunny" size={0} />);
    expect(zeroContainer.querySelector('svg')?.getAttribute('width')).toBe('20');

    const { container: unknownContainer } = render(<WeatherIcon name="unsupported <script>" />);
    expect(
      unknownContainer.querySelector('svg')?.classList.contains('mm-icon-unsupportedscript'),
    ).toBe(true);

    const { container: pureSpecialContainer } = render(<WeatherIcon name="###$$$@@@" />);
    expect(
      pureSpecialContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy'),
    ).toBe(true);
  });

  it('applies custom className and exercises memo cache on prop update', () => {
    const { container, rerender } = render(
      <WeatherIcon name="weather-sunny" className="custom-test-class" />,
    );
    expect(container.querySelector('svg')?.classList.contains('custom-test-class')).toBe(true);

    rerender(<WeatherIcon name="weather-sunny" className="updated-test-class" />);
    expect(container.querySelector('svg')?.classList.contains('updated-test-class')).toBe(true);
  });
});

describe('WeatherHourlyGraph component', () => {

  it('renders nothing when hourly data is empty or invalid', () => {
    const { container: emptyContainer } = render(<WeatherHourlyGraph hourly={[]} />);
    expect(emptyContainer.firstChild).toBeNull();

    const { container: nullContainer } = render(<WeatherHourlyGraph hourly={null} />);
    expect(nullContainer.firstChild).toBeNull();
  });

  it('renders declarative SVG graph with timeline points and icons', () => {
    const hourly = [
      { time: '2026-10-31T12:00:00Z', temp: 68, precip_prob: 0, icon: 'weather-sunny' },
      { time: '2026-10-31T13:00:00Z', temp: 70, precip_prob: 25, icon: 'weather-rainy' },
    ];
    const { container, rerender } = render(
      <WeatherHourlyGraph hourly={hourly} widgetId="test-w" />,
    );
    const svg = container.querySelector('svg.weather-hourly-graph');
    expect(svg).not.toBeNull();
    expect(svg?.querySelector('linearGradient#mm-weather-grad-test-w')).not.toBeNull();
    expect(svg?.querySelector('path[d^="M"]')).not.toBeNull();
    expect(container.textContent).toContain('Now');
    // Exercise memoization cache hit on rerender with changed widgetId but identical hourly reference
    rerender(<WeatherHourlyGraph hourly={hourly} widgetId="test-w2" />);
    expect(container.querySelector('svg.weather-hourly-graph')).not.toBeNull();
    expect(container.textContent).toContain('☂ 25%');
    expect(container.textContent).toContain('68°');
    expect(container.textContent).toContain('70°');
  });

  it('fits 24 hourly points inside a narrow card without pixel minWidth clipping (#867)', () => {
    const cardWidth = 480;
    const pts = Array.from({ length: 24 }, (_, i) => ({
      time: `2026-09-26T${String(i).padStart(2, '0')}:00`,
      temp: 55 + (i % 10),
      icon: 'weather-sunny',
    }));
    const { container } = render(
      <div style={{ width: `${cardWidth}px` }}>
        <WeatherHourlyGraph
          hourly={pts}
          widgetId="narrow-card"
          options={{ containerWidth: cardWidth }}
        />
      </div>,
    );
    const svg = container.querySelector('svg.weather-hourly-graph') as SVGSVGElement;
    expect(svg).not.toBeNull();
    expect(svg.style.minWidth).toBe('');
    expect(svg.style.width).toBe('100%');
    expect(svg.style.maxWidth).toBe('100%');
    expect(svg.getAttribute('viewBox')).toBe('0 0 480.0 270.0');

    const labels = Array.from(svg.querySelectorAll('text'));
    const lastHourLabel = labels.find((el) => el.textContent === '9 AM');
    expect(lastHourLabel).toBeDefined();
    const lastX = Number(lastHourLabel?.getAttribute('x'));
    expect(lastX).toBeGreaterThan(0);
    expect(lastX).toBeLessThan(cardWidth);
  });

  it('renders WeatherHourlyGraph when gridLinesPath, areaPath, curvePath are empty or present with precipitation', () => {
    // 1. All paths present with non-zero precipitation
    const spy1 = vi.spyOn(formatModule, 'computeHourlyGraphLayout').mockReturnValueOnce({
      totalWidth: 500, svgHeight: 200, baseY: 180, isTall: false, gradientId: 'grad-full',
      areaPath: 'M 0 180 L 100 100 L 200 180 Z', curvePath: 'M 0 100 L 100 100',
      gridLinesPath: 'M 50 0 L 50 180',
      points: [{
        x: 50, y: 50, lineY1: 54, precipProb: 40, precipY: 12, icon: 'weather-rainy',
        iconTranslateX: 40, iconY: 14, iconScale: 0.8, tempRound: 65, tempY: 44,
        tempFontSize: 12, circleR: 3.5, timeLabel: 'Now', timeWeight: '800',
        timeColor: '#000', timeY: 102, timeFontSize: 11,
      }],
    });
    const { container: cFull } = render(<WeatherHourlyGraph hourly={[{ temp: 65 }]} />);
    expect(cFull.querySelector('svg.weather-hourly-graph')).not.toBeNull();
    expect(cFull.textContent).toContain('☂ 40%');
    expect(cFull.querySelectorAll('path').length).toBeGreaterThanOrEqual(3);
    spy1.mockRestore();

    // 2. All paths empty / falsy and precipProb = 0
    const spy2 = vi.spyOn(formatModule, 'computeHourlyGraphLayout').mockReturnValueOnce({
      totalWidth: 500, svgHeight: 200, baseY: 180, isTall: false, gradientId: 'grad-empty',
      areaPath: '', curvePath: '', gridLinesPath: '',
      points: [{
        x: 50, y: 50, lineY1: 54, precipProb: 0, precipY: 12, icon: 'weather-sunny',
        iconTranslateX: 40, iconY: 14, iconScale: 0.8, tempRound: 65, tempY: 44,
        tempFontSize: 12, circleR: 3.5, timeLabel: 'Now', timeWeight: '800',
        timeColor: '#000', timeY: 102, timeFontSize: 11,
      }],
    });
    const { container: cEmpty } = render(<WeatherHourlyGraph hourly={[{ temp: 65 }]} />);
    expect(cEmpty.querySelector('svg.weather-hourly-graph')).not.toBeNull();
    expect(cEmpty.textContent).not.toContain('☂');
    // Only the icon inside point glyph has a path; areaPath, curvePath, gridLinesPath are absent
    expect(cEmpty.querySelectorAll('path').length).toBe(1);
    spy2.mockRestore();
  });

  it('renders WeatherIcon size bounds, custom class, and glyph variations', () => {
    // Clamping > 64 to 64
    const { container: c1 } = render(
      <WeatherIcon name="weather-sunny" size={100} className="large-icon" />,
    );
    const svg1 = c1.querySelector('svg');
    expect(svg1?.getAttribute('width')).toBe('64');
    expect(svg1?.getAttribute('height')).toBe('64');
    expect(svg1?.classList.contains('large-icon')).toBe(true);

    // Clamping < 12 to 12
    const { container: c2 } = render(<WeatherIcon name="weather-cloudy" size={5} />);
    const svg2 = c2.querySelector('svg');
    expect(svg2?.getAttribute('width')).toBe('12');
    expect(svg2?.getAttribute('height')).toBe('12');

    // Invalid / NaN / negative size defaults to 20
    const { container: cDef1 } = render(<WeatherIcon name="weather-sunny" size={-10} />);
    expect(cDef1.querySelector('svg')?.getAttribute('width')).toBe('20');
    const { container: cDef2 } = render(<WeatherIcon name="weather-sunny" size={NaN} />);
    expect(cDef2.querySelector('svg')?.getAttribute('width')).toBe('20');

    // All WeatherIconGlyph branches
    const glyphs = [
      'weather-sunny', 'weather-clear-night', 'weather-partly-cloudy', 'weather-night-partly-cloudy',
      'weather-cloudy', 'weather-fog', 'weather-rainy', 'weather-pouring',
      'weather-snowy', 'weather-snowy-rainy', 'weather-lightning', 'weather-lightning-rainy',
    ];
    for (const glyph of glyphs) {
      const { container } = render(<WeatherIcon name={glyph} />);
      expect(container.querySelector('svg')).not.toBeNull();
      const { container: glyphContainer } = render(<svg><WeatherIconGlyph name={glyph} /></svg>);
      expect(glyphContainer.querySelector('path')).not.toBeNull();
    }

    // Name normalization: empty, whitespace, null, undefined, special chars sanitization
    const { container: cEmpty } = render(<WeatherIcon name="" />);
    expect(cEmpty.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);
    const { container: cSpaces } = render(<WeatherIcon name="   " />);
    expect(cSpaces.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);
    const { container: cNull } = render(<WeatherIcon name={null} />);
    expect(cNull.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);
    const { container: cSpecial } = render(<WeatherIcon name="weather$$sunny!!" />);
    expect(cSpecial.querySelector('svg')?.classList.contains('mm-icon-weathersunny')).toBe(true);
    const { container: cAllSpecial } = render(<WeatherIcon name="$$$###@@@" />);
    expect(cAllSpecial.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);

    // WeatherIconGlyph standalone direct render inside SVG with special characters
    const { container: cGlyphSpecial } = render(<svg><WeatherIconGlyph name="weather#fog" /></svg>);
    expect(cGlyphSpecial.querySelector('path')).not.toBeNull();

    // WeatherIconGlyph default branch for unrecognized tokens
    const { container: cGlyphDefault } = render(<svg><WeatherIconGlyph name="weather-tornado" /></svg>);
    expect(cGlyphDefault.querySelector('path')).not.toBeNull();

    // WeatherIconGlyph with null / whitespace / non-matching characters
    const { container: cGlyphNull } = render(<svg><WeatherIconGlyph name={null} /></svg>);
    expect(cGlyphNull.querySelector('path')).not.toBeNull();
    const { container: cGlyphEmpty } = render(<svg><WeatherIconGlyph name="   " /></svg>);
    expect(cGlyphEmpty.querySelector('path')).not.toBeNull();
    const { container: cGlyphSymbols } = render(<svg><WeatherIconGlyph name="$$$" /></svg>);
    expect(cGlyphSymbols.querySelector('path')).not.toBeNull();
  });

  it('exercises React compiler memoization on WeatherIcon and WeatherIconGlyph across all tokens', () => {
    const allTokens = [
      'weather-sunny', 'weather-clear-night', 'weather-partly-cloudy', 'weather-night-partly-cloudy',
      'weather-cloudy', 'weather-fog', 'weather-rainy', 'weather-pouring',
      'weather-snowy', 'weather-snowy-rainy', 'weather-lightning', 'weather-lightning-rainy',
      'unknown-token', '',
    ];

    for (const token of allTokens) {
      const { rerender: rerenderGlyph, unmount: unmountGlyph } = render(<svg><WeatherIconGlyph name={token} /></svg>);
      rerenderGlyph(<svg><WeatherIconGlyph name={token} /></svg>);
      unmountGlyph();

      const { rerender: rerenderIcon, unmount: unmountIcon } = render(
        <WeatherIcon name={token} size={24} className="test-icon" />,
      );
      rerenderIcon(<WeatherIcon name={token} size={24} className="test-icon" />);
      unmountIcon();
    }
  });

  it('renders widget error banner when widget state is error', () => {
    const { container } = renderWeather('weather-error-widget', {}, { current: { temperature: 65 } }, 'error');
    expect(container.querySelector('.widget-error-banner')).not.toBeNull();
    expect(container.querySelector('.widget-error-banner')?.textContent).toContain('Widget Error');
  });

  it('handles hourly slot resolution when time strings are missing and exercises hourly index fallback', () => {
    const { container } = renderWeather('weather-hourly-no-time', SCHOOL_DAY, {
      current: { Temperature: 64 },
      hourly: [{ Temp: 58, PrecipProb: 10 }, { Temp: 68, PrecipProb: 20 }, { Temp: 62, PrecipProb: 30 }],
    });
    const strip = container.querySelector('.weather-school-day-strip');
    expect(strip).not.toBeNull();
    const morningSlot = strip?.querySelector('.weather-school-day-slot-morning');
    expect(morningSlot?.textContent).toContain('58°');
    const afternoonSlot = strip?.querySelector('.weather-school-day-slot-afternoon');
    expect(afternoonSlot?.textContent).toContain('68°');
    const eveningSlot = strip?.querySelector('.weather-school-day-slot-evening');
    expect(eveningSlot?.textContent).toContain('62°');
  });

  it('handles school-day mode with fallback slot weather using PascalCase Temperature without daily or hourly', () => {
    const { container } = renderWeather('weather-fallback-pascal', SCHOOL_DAY, {
      current: { Temperature: 55, ConditionText: 'Cloudy' },
    });
    const strip = container.querySelector('.weather-school-day-strip');
    expect(strip).not.toBeNull();
    expect(strip?.textContent).toContain('55°');
  });

  it('rolls over school-day strip to tomorrow at night (>= 19:00) using tomorrow hourly and daily data', () => {
    seedWeatherAt('weather-school-tomorrow', SCHOOL_DAY, {
      current: { temperature: 56, condition_text: 'Clear', icon: 'weather-clear-night' },
      hourly: [
        { time: '2026-10-31T20:00:00Z', temp: 55, precip_prob: 5, icon: 'weather-clear-night' },
        { time: '2026-11-01T08:00:00Z', temp: 46, precip_prob: 40, icon: 'weather-rainy' },
        { time: '2026-11-01T14:00:00Z', temp: 68, precip_prob: 15, icon: 'weather-partly-cloudy' },
        { time: '2026-11-01T18:00:00Z', temp: 58, precip_prob: 0, icon: 'weather-sunny' },
      ],
    }, '2026-10-31T20:15:00Z');
    const { container } = renderWidget(WeatherForecastWidget, 'weather-school-tomorrow', WF);

    const strip = container.querySelector('.weather-school-day-strip');
    expect(strip?.getAttribute('data-day')).toBe('tomorrow');

    const morningSlot = container.querySelector('.weather-school-day-slot-morning');
    expect(morningSlot?.getAttribute('data-day')).toBe('tomorrow');
    expect(morningSlot?.querySelector('.weather-school-day-name')?.textContent).toBe('tomorrow morning');
    expect(morningSlot?.querySelector('.weather-school-day-temp')?.textContent).toBe('46°');
    expect(morningSlot?.querySelector('.weather-school-day-outfit')?.textContent).toBe('Raincoat & Umbrella');
    expect(morningSlot?.querySelector('.weather-school-day-forecast')?.textContent).toBe('Rain Showers · 40% rain');

    const afternoonSlot = container.querySelector('.weather-school-day-slot-afternoon');
    expect(afternoonSlot?.querySelector('.weather-school-day-name')?.textContent).toBe('tomorrow afternoon');
    expect(afternoonSlot?.querySelector('.weather-school-day-temp')?.textContent).toBe('68°');
    expect(afternoonSlot?.querySelector('.weather-school-day-outfit')?.textContent).toBe('T-Shirt & Long Pants');
    expect(afternoonSlot?.querySelector('.weather-school-day-forecast')?.textContent).toBe('Partly Cloudy · 15% rain');

    const eveningSlot = container.querySelector('.weather-school-day-slot-evening');
    expect(eveningSlot?.querySelector('.weather-school-day-name')?.textContent).toBe('tomorrow evening');
    expect(eveningSlot?.querySelector('.weather-school-day-temp')?.textContent).toBe('58°');
    expect(eveningSlot?.querySelector('.weather-school-day-outfit')?.textContent).toBe('Hoodie or Fleece');
    expect(eveningSlot?.querySelector('.weather-school-day-forecast')?.textContent).toBe('Sunny · 0% rain');
  });

  it('uses daily[1] fallback when rolling over to tomorrow at night without hourly data', () => {
    seedWeatherAt('weather-school-tomorrow-daily', SCHOOL_DAY, {
      current: { temperature: 54, icon: 'weather-clear-night' },
      daily: [
        { temp_max: 65, temp_min: 50, date: '2026-10-31' },
        { temp_max: 80, temp_min: 44, date: '2026-11-01' },
      ],
    }, '2026-10-31T19:00:00Z');
    const { container } = renderWidget(WeatherForecastWidget, 'weather-school-tomorrow-daily', WF);

    const morningSlot = container.querySelector('.weather-school-day-slot-morning');
    expect(morningSlot?.querySelector('.weather-school-day-temp')?.textContent).toBe('44°');
    expect(morningSlot?.querySelector('.weather-school-day-outfit')?.textContent).toBe('Warm Puffer Coat');

    const afternoonSlot = container.querySelector('.weather-school-day-slot-afternoon');
    expect(afternoonSlot?.querySelector('.weather-school-day-temp')?.textContent).toBe('80°');
    expect(afternoonSlot?.querySelector('.weather-school-day-outfit')?.textContent).toBe('Shorts or Sun Dress');
  });

  it('evaluates schoolDayPack outfit labels, icon conditions, and date rollover helpers', () => {
    expect(resolveClothingOutfitLabel('morning_rain')).toBe('Raincoat & Umbrella');
    expect(resolveClothingOutfitLabel('rainy_boots')).toBe('Raincoat & Boots');
    expect(resolveClothingOutfitLabel('cold_puffer')).toBe('Warm Puffer Coat');
    expect(resolveClothingOutfitLabel('morning_mild')).toBe('Light Jacket & Pants');
    expect(resolveClothingOutfitLabel('cool_evening')).toBe('Hoodie or Fleece');
    expect(resolveClothingOutfitLabel('warm_pants')).toBe('T-Shirt & Long Pants');
    expect(resolveClothingOutfitLabel('hot_dress')).toBe('Shorts or Sun Dress');

    expect(resolveIconConditionLabel('weather-pouring')).toBe('Heavy Rain');
    expect(resolveIconConditionLabel('weather-lightning-rainy')).toBe('Thunderstorms');
    expect(resolveIconConditionLabel('weather-snowy')).toBe('Snow');
    expect(resolveIconConditionLabel('weather-rainy')).toBe('Rain Showers');
    expect(resolveIconConditionLabel('weather-fog')).toBe('Foggy');
    expect(resolveIconConditionLabel('weather-partly-cloudy')).toBe('Partly Cloudy');
    expect(resolveIconConditionLabel('weather-cloudy')).toBe('Cloudy');
    expect(resolveIconConditionLabel('weather-clear-night')).toBe('Clear');
    expect(resolveIconConditionLabel('weather-sunny')).toBe('Sunny');

    expect(formatSlotForecastSummary('weather-partly-cloudy', 25)).toBe('Partly Cloudy · 25% rain');
    expect(formatSlotForecastSummary('weather-sunny', NaN)).toBe('Sunny · 0% rain');

    expect(addOneCalendarDay('2026-10-31')).toBe('2026-11-01');
    expect(addOneCalendarDay('invalid-date')).toBe('invalid-date');

    expect(resolveSchoolDayTargetDate({ date: '2026-12-25' }, '2026-10-31T22:00:00Z')).toEqual({
      dateStr: '2026-12-25',
      isTomorrow: false,
    });
    expect(resolveSchoolDayTargetDate({}, '2026-10-31T19:05:00Z')).toEqual({
      dateStr: '2026-11-01',
      isTomorrow: true,
    });
    expect(
      resolveSchoolDayTargetDate({ timezone: 'America/Los_Angeles' }, '2026-11-03T04:15:00Z'),
    ).toEqual({
      dateStr: '2026-11-03',
      isTomorrow: true,
    });
    expect(
      resolveSchoolDayTargetDate({ timezone: 'Invalid/Timezone' }, '2026-10-31T19:05:00Z'),
    ).toEqual({
      dateStr: '2026-11-01',
      isTomorrow: true,
    });
    expect(resolveSchoolDayTargetDate({}, '2026-10-31')).toEqual({
      dateStr: '2026-10-31',
      isTomorrow: false,
    });
  });

  it('renders configurable birthday override badge, outfit prefix, and birthday sprite URLs when birthdays match', () => {
    const birthdayConfig = { mode: 'school-day', character_pack: 'ghibli_school_day', birthdays: [{ name: 'ChildOne', date: '11-07' }] };
    const birthdayData = {
      current: { temperature: 65, icon: 'weather-sunny' },
      slots: {
        morning: { temp: 52, precip_prob: 0, icon: 'weather-sunny' },
        afternoon: { temp: 72, precip_prob: 0, icon: 'weather-sunny' },
        evening: { temp: 60, precip_prob: 0, icon: 'weather-clear-night' },
      },
    };
    seedWeatherAt('weather-school-birthday', { ...birthdayConfig, date: '2026-11-07' }, birthdayData, '2026-11-07T09:00:00Z');
    const { container, rerender } = renderWidget(WeatherForecastWidget, 'weather-school-birthday', WF);

    const morningSlot = container.querySelector('.weather-school-day-slot-morning');
    expect(morningSlot?.getAttribute('data-birthday')).toBe('true');
    expect(morningSlot?.querySelector('.weather-school-day-birthday-badge')?.textContent).toBe(
      '🎂 Happy Birthday ChildOne!',
    );
    expect(morningSlot?.querySelector('.weather-school-day-outfit')?.textContent).toBe(
      '🎂 Birthday Celebration! · Light Jacket & Pants',
    );
    expect(morningSlot?.querySelector('img.weather-school-day-sprite')?.getAttribute('src')).toBe(
      '/static/ui/assets/weather-packs/ghibli_school_day/birthday_childone_morning.jpg',
    );

    const afternoonSlot = container.querySelector('.weather-school-day-slot-afternoon');
    expect(afternoonSlot?.querySelector('img.weather-school-day-sprite')?.getAttribute('src')).toBe(
      '/static/ui/assets/weather-packs/ghibli_school_day/birthday_childone_afternoon.jpg',
    );

    const eveningSlot = container.querySelector('.weather-school-day-slot-evening');
    expect(eveningSlot?.querySelector('img.weather-school-day-sprite')?.getAttribute('src')).toBe(
      '/static/ui/assets/weather-packs/ghibli_school_day/birthday_childone_evening.jpg',
    );

    // Non-matching date should not render birthday override
    seedWeatherAt('weather-school-birthday', { ...birthdayConfig, date: '2026-11-08' }, birthdayData, '2026-11-08T09:00:00Z');

    rerender(<WeatherForecastWidget id="weather-school-birthday" type="weather-forecast" />);
    const nonBirthdayMorning = container.querySelector('.weather-school-day-slot-morning');
    expect(nonBirthdayMorning?.getAttribute('data-birthday')).toBeNull();
    expect(nonBirthdayMorning?.querySelector('.weather-school-day-birthday-badge')).toBeNull();
    expect(nonBirthdayMorning?.querySelector('.weather-school-day-outfit')?.textContent).toBe(
      'Light Jacket & Pants',
    );
    expect(
      nonBirthdayMorning?.querySelector('img.weather-school-day-sprite')?.getAttribute('src'),
    ).toMatch(/^\/static\/ui\/assets\/weather-packs\/ghibli_school_day\/morning_mild_v\d+\.jpg$/);
  });

  it('applies custom shadow_opacity config as --weather-scrim-opacity on .weather-school-day-strip', () => {
    const { container, rerender } = renderWeather('weather-school-shadow', { mode: 'school-day', shadow_opacity: 0.9 }, {
      current: { temperature: 68, icon: 'weather-sunny' },
    });

    const strip = container.querySelector<HTMLElement>('.weather-school-day-strip');
    expect(strip).not.toBeNull();
    expect(strip?.style.getPropertyValue('--weather-scrim-opacity')).toBe('0.9');

    // Out-of-range shadow_opacity (> 1) should be ignored
    seedWidget('weather-school-shadow', widgetMeta(WF, { mode: 'school-day', shadow_opacity: 1.5 }), {
      current: { temperature: 68, icon: 'weather-sunny' },
    });

    rerender(<WeatherForecastWidget id="weather-school-shadow" type="weather-forecast" />);
    const stripInvalid = container.querySelector<HTMLElement>('.weather-school-day-strip');
    expect(stripInvalid?.style.getPropertyValue('--weather-scrim-opacity')).toBe('');
  });

  describe('daily_manifest.json and style badge support', () => {
    it('renders daily_manifest sprites and style badge on matching target_date', async () => {
      const manifest = {
        target_date: '2026-11-17',
        theme_title: 'Autumn Rain',
        style_label: 'Watercolor Ghibli',
        slots: {
          morning: 'daily_2026-11-17_morning.jpg',
          afternoon: 'daily_2026-11-17_afternoon.jpg',
          evening: 'daily_2026-11-17_evening.jpg',
        },
      };

      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: true,
        json: async () => manifest,
      } as Response);

      const { container } = renderWeather('weather-manifest-test', { mode: 'school-day', character_pack: 'ghibli_custom', date: '2026-11-17' }, {
        current: { temperature: 68, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        const badge = container.querySelector('.weather-school-day-style-badge');
        expect(badge).not.toBeNull();
        expect(badge?.textContent).toBe('Watercolor Ghibli');
      });

      const morningImg = container.querySelector(
        '.weather-school-day-slot-morning img.weather-school-day-sprite',
      );
      expect(morningImg?.getAttribute('src')).toBe(
        '/static/ui/assets/weather-packs/ghibli_custom/daily_2026-11-17_morning.jpg',
      );

      const afternoonImg = container.querySelector(
        '.weather-school-day-slot-afternoon img.weather-school-day-sprite',
      );
      expect(afternoonImg?.getAttribute('src')).toBe(
        '/static/ui/assets/weather-packs/ghibli_custom/daily_2026-11-17_afternoon.jpg',
      );

      const eveningSlot = container.querySelector('.weather-school-day-slot-evening');
      const eveningImg = eveningSlot?.querySelector('img.weather-school-day-sprite');
      expect(eveningImg?.getAttribute('src')).toBe(
        '/static/ui/assets/weather-packs/ghibli_custom/daily_2026-11-17_evening.jpg',
      );
      expect(eveningSlot?.querySelector('.weather-school-day-style-badge')?.textContent).toBe(
        'Watercolor Ghibli',
      );
    });

    it('falls back cleanly to static character pack sprites without style badge on 404 or fetch failure', async () => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: false,
        status: 404,
      } as Response);

      const { container } = renderWeather('weather-manifest-404', { mode: 'school-day', character_pack: 'ghibli_school_day', date: '2026-11-17' }, {
        current: { temperature: 68, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        expect(globalThis.fetch).toHaveBeenCalled();
      });

      const strip = container.querySelector('.weather-school-day-strip');
      expect(strip).not.toBeNull();
      expect(container.querySelector('.weather-school-day-style-badge')).toBeNull();

      const sprites = container.querySelectorAll('img.weather-school-day-sprite');
      expect(sprites.length).toBe(3);
      for (const sprite of sprites) {
        expect(sprite.getAttribute('src')).toMatch(
          /^\/static\/ui\/assets\/weather-packs\/ghibli_school_day\/[a-z_]+_v\d+\.jpg$/,
        );
      }
    });

    it('falls back to static sprites without badge when daily_manifest target_date is stale', async () => {
      const staleManifest = {
        target_date: '2026-11-16',
        style_label: 'Yesterday Style',
        slots: {
          morning: 'daily_2026-11-16_morning.jpg',
        },
      };

      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: true,
        json: async () => staleManifest,
      } as Response);

      const { container } = renderWeather('weather-manifest-stale', { mode: 'school-day', character_pack: 'ghibli_school_day', date: '2026-11-17' }, {
        current: { temperature: 68, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        expect(globalThis.fetch).toHaveBeenCalled();
      });

      expect(container.querySelector('.weather-school-day-style-badge')).toBeNull();
      const morningImg = container.querySelector(
        '.weather-school-day-slot-morning img.weather-school-day-sprite',
      );
      expect(morningImg?.getAttribute('src')).toMatch(/_v\d+\.jpg$/);
    });

    it('preserves birthday override priority over daily manifest', async () => {
      const birthdayManifest = {
        target_date: '2026-11-07',
        style_label: 'Storybook Theme',
        slots: {
          morning: 'daily_2026-11-07_morning.jpg',
          afternoon: 'daily_2026-11-07_afternoon.jpg',
          evening: 'daily_2026-11-07_evening.jpg',
        },
      };

      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: true,
        json: async () => birthdayManifest,
      } as Response);

      const { container } = renderWeather('weather-manifest-bday', { mode: 'school-day', character_pack: 'ghibli_school_day', date: '2026-11-07', birthdays: [{ name: 'ChildOne', date: '11-07' }] }, {
        current: { temperature: 68, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        expect(globalThis.fetch).toHaveBeenCalled();
      });

      const morningSlot = container.querySelector('.weather-school-day-slot-morning');
      expect(morningSlot?.getAttribute('data-birthday')).toBe('true');
      expect(morningSlot?.querySelector('.weather-school-day-birthday-badge')?.textContent).toBe(
        '🎂 Happy Birthday ChildOne!',
      );
      expect(morningSlot?.querySelector('img.weather-school-day-sprite')?.getAttribute('src')).toBe(
        '/static/ui/assets/weather-packs/ghibli_school_day/birthday_childone_morning.jpg',
      );
      expect(container.querySelector('.weather-school-day-style-badge')).toBeNull();
    });

    it('uses US holiday sprite and hides style badge when allow_on_holiday is not true', async () => {
      const holidayManifestNoAllow = {
        target_date: '2026-07-04',
        style_label: 'Patriotic Theme',
        slots: {
          morning: 'daily_2026-07-04_morning.jpg',
        },
      };

      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: true,
        json: async () => holidayManifestNoAllow,
      } as Response);

      const { container } = renderWeather('weather-manifest-holiday-block', { mode: 'school-day', character_pack: 'ghibli_school_day', date: '2026-07-04', holidays: true }, {
        current: { temperature: 80, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        expect(globalThis.fetch).toHaveBeenCalled();
      });

      const morningImg = container.querySelector(
        '.weather-school-day-slot-morning img.weather-school-day-sprite',
      );
      expect(morningImg?.getAttribute('src')).toContain('holiday_july_4th_');
      expect(container.querySelector('.weather-school-day-style-badge')).toBeNull();
    });

    it('uses daily manifest sprite and shows style badge when allow_on_holiday is true', async () => {
      const holidayManifestAllowed = {
        target_date: '2026-07-04',
        style_label: 'Allowed On Holiday',
        allow_on_holiday: true,
        slots: {
          morning: 'daily_2026-07-04_morning.jpg',
          afternoon: 'daily_2026-07-04_afternoon.jpg',
          evening: 'daily_2026-07-04_evening.jpg',
        },
      };

      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: true,
        json: async () => holidayManifestAllowed,
      } as Response);

      const { container } = renderWeather('weather-manifest-holiday-allow', { mode: 'school-day', character_pack: 'ghibli_school_day', date: '2026-07-04', holidays: true }, {
        current: { temperature: 80, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        const badge = container.querySelector('.weather-school-day-style-badge');
        expect(badge).not.toBeNull();
        expect(badge?.textContent).toBe('Allowed On Holiday');
      });

      const allowedMorningImg = container.querySelector(
        '.weather-school-day-slot-morning img.weather-school-day-sprite',
      );
      expect(allowedMorningImg?.getAttribute('src')).toBe(
        '/static/ui/assets/weather-packs/ghibli_school_day/daily_2026-07-04_morning.jpg',
      );
    });

    it('handles non-string style_label and theme_title in daily_manifest without throwing or rendering style badge', async () => {
      const malformedLabelManifest = {
        target_date: '2026-11-17',
        style_label: 123,
        theme_title: { title: 'Object Title' },
        slots: {
          morning: 'daily_2026-11-17_morning.jpg',
          afternoon: 'daily_2026-11-17_afternoon.jpg',
          evening: 'daily_2026-11-17_evening.jpg',
        },
      };

      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: true,
        json: async () => malformedLabelManifest,
      } as Response);

      const { container } = renderWeather('weather-manifest-malformed-labels', { mode: 'school-day', character_pack: 'ghibli_custom', date: '2026-11-17' }, {
        current: { temperature: 68, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        const morningImg = container.querySelector(
          '.weather-school-day-slot-morning img.weather-school-day-sprite',
        );
        expect(morningImg?.getAttribute('src')).toBe(
          '/static/ui/assets/weather-packs/ghibli_custom/daily_2026-11-17_morning.jpg',
        );
      });

      expect(container.querySelector('.weather-school-day-style-badge')).toBeNull();
    });

    it('falls back to static sprites without style badge when daily_manifest has valid style_label but no usable slot images', async () => {
      const emptySlotsManifest = {
        target_date: '2026-11-17',
        style_label: 'Watercolor Storybook',
        slots: {
          morning: '../invalid-traversal.jpg',
          afternoon: '   ',
        },
      };

      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
        ok: true,
        json: async () => emptySlotsManifest,
      } as Response);

      const { container } = renderWeather('weather-manifest-no-slots', { mode: 'school-day', character_pack: 'ghibli_school_day', date: '2026-11-17' }, {
        current: { temperature: 68, icon: 'weather-sunny' },
      });

      await waitFor(() => {
        expect(globalThis.fetch).toHaveBeenCalled();
      });

      expect(container.querySelector('.weather-school-day-style-badge')).toBeNull();
      const sprites = container.querySelectorAll('img.weather-school-day-sprite');
      expect(sprites.length).toBe(3);
      for (const sprite of sprites) {
        expect(sprite.getAttribute('src')).toMatch(
          /^\/static\/ui\/assets\/weather-packs\/ghibli_school_day\/[a-z_]+_v\d+\.jpg$/,
        );
      }
    });
  });
});
