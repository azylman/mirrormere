import { cleanup, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useDisplayStore } from '../../store';
import { WeatherForecastWidget } from './WeatherForecastWidget';
import { WeatherIcon } from './WeatherIcon';
import { WeatherHourlyGraph } from './WeatherHourlyGraph';

const initialStore = useDisplayStore.getState();

describe('WeatherForecastWidget', () => {
  beforeEach(() => {
    useDisplayStore.setState(initialStore, true);
  });

  afterEach(() => {
    cleanup();
  });

  it('renders unnamed loading state without a header when data is {}', () => {
    useDisplayStore.setState({
      widgets: {
        'weather-unnamed': {
          meta: {
            type: 'weather-forecast',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <WeatherForecastWidget id="weather-unnamed" type="weather-forecast" />,
    );
    const root = container.querySelector('.widget-weather');
    expect(root).not.toBeNull();
    expect(root?.querySelector('.weather-header')).toBeNull();
    expect(root?.querySelector('.weather-loading')?.textContent).toContain(
      'Loading weather forecast...',
    );
  });

  it('renders named loading state with header when config.location_name is set but data is unhydrated', () => {
    useDisplayStore.setState({
      widgets: {
        'weather-named': {
          meta: {
            type: 'weather-forecast',
            origin: [0, 0],
            dimensions: [3, 2],
            config: { location_name: 'San Francisco' },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <WeatherForecastWidget id="weather-named" type="weather-forecast" />,
    );
    const root = container.querySelector('.widget-weather');
    expect(root?.querySelector('.weather-header')).not.toBeNull();
    expect(root?.querySelector('.weather-location-name')?.textContent).toBe('San Francisco');
    expect(root?.querySelector('.weather-loading')?.textContent).toContain(
      'Loading weather forecast...',
    );
  });

  it('renders full current conditions and hourly SVG graph with snake_case payload', () => {
    useDisplayStore.setState({
      widgets: {
        'weather-snake': {
          meta: {
            type: 'weather-forecast',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {
            location: 'Oakland',
            current: {
              temperature: 68.4,
              condition_text: 'Sunny',
              icon: 'weather-sunny',
              humidity: 55,
              wind_speed: 12.3,
              units: {
                temperature: '°F',
                wind_speed: 'mph',
              },
            },
            daily: [
              {
                temp_max: 74.2,
                temp_min: 52.8,
                precip_prob_max: 10,
              },
            ],
            hourly: [
              { time: '2026-10-31T12:00:00Z', temp: 68, precip_prob: 0, icon: 'weather-sunny' },
              { time: '2026-10-31T13:00:00Z', temp: 70, precip_prob: 5, icon: 'weather-sunny' },
              {
                time: '2026-10-31T14:00:00Z',
                temp: 72,
                precip_prob: 10,
                icon: 'weather-partly-cloudy',
              },
            ],
          },
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <WeatherForecastWidget id="weather-snake" type="weather-forecast" />,
    );
    const root = container.querySelector('.widget-weather');
    expect(root).not.toBeNull();

    // Header
    expect(root?.querySelector('.weather-location-name')?.textContent).toBe('Oakland');
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
    useDisplayStore.setState({
      widgets: {
        'weather-pascal': {
          meta: {
            type: 'weather-forecast',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {
            Location: 'Seattle',
            Current: {
              Temperature: 54.6,
              ConditionText: 'Rainy',
              Icon: 'weather-rainy',
              Humidity: 88,
              WindSpeed: 8.7,
              Units: {
                Temperature: '°',
                WindSpeed: 'knots',
              },
            },
            Daily: [
              {
                TempMax: 58.1,
                TempMin: 49.3,
                PrecipProbMax: 80,
              },
            ],
            Hourly: [
              { Time: '2026-10-31T12:00:00Z', Temp: 54, PrecipProb: 80, Icon: 'weather-rainy' },
              { Time: '2026-10-31T13:00:00Z', Temp: 55, PrecipProb: 75, Icon: 'weather-rainy' },
            ],
          },
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <WeatherForecastWidget id="weather-pascal" type="weather-forecast" />,
    );
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
    useDisplayStore.setState({
      widgets: {
        'weather-no-hourly': {
          meta: {
            type: 'weather-forecast',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {
            current: {
              temperature: 70,
              condition_text: 'Clear',
              icon: 'weather-sunny',
            },
            hourly: [],
          },
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <WeatherForecastWidget id="weather-no-hourly" type="weather-forecast" />,
    );
    const empty = container.querySelector('.weather-empty');
    expect(empty).not.toBeNull();
    expect(empty?.textContent).toContain('No hourly forecast available');
  });

  it('renders widget-error-banner when state is error', () => {
    useDisplayStore.setState({
      widgets: {
        'weather-err': {
          state: 'error',
          data: {},
          timestamp: '',
        },
      },
    });

    const { container } = render(
      <WeatherForecastWidget id="weather-err" type="weather-forecast" />,
    );
    const banner = container.querySelector('.widget-error-banner');
    expect(banner).not.toBeNull();
    expect(banner?.textContent).toContain('Widget Error');
  });
});

describe('WeatherIcon component', () => {
  afterEach(() => {
    cleanup();
  });

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
    expect(nullContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);

    const { container: unknownContainer } = render(<WeatherIcon name="unsupported <script>" />);
    expect(unknownContainer.querySelector('svg')?.classList.contains('mm-icon-unsupportedscript')).toBe(true);
  });

  it('applies custom className', () => {
    const { container } = render(<WeatherIcon name="weather-sunny" className="custom-test-class" />);
    expect(container.querySelector('svg')?.classList.contains('custom-test-class')).toBe(true);
  });
});

describe('WeatherHourlyGraph component', () => {
  afterEach(() => {
    cleanup();
  });

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
    const { container } = render(<WeatherHourlyGraph hourly={hourly} widgetId="test-w" />);
    const svg = container.querySelector('svg.weather-hourly-graph');
    expect(svg).not.toBeNull();
    expect(svg?.querySelector('linearGradient#mm-weather-grad-test-w')).not.toBeNull();
    expect(svg?.querySelector('path[d^="M"]')).not.toBeNull();
    expect(container.textContent).toContain('Now');
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
    expect(svg.getAttribute('viewBox')).toBe('0 0 480.0 300.0');

    const labels = Array.from(svg.querySelectorAll('text'));
    const lastHourLabel = labels.find((el) => el.textContent === '11 PM');
    expect(lastHourLabel).toBeDefined();
    const lastX = Number(lastHourLabel?.getAttribute('x'));
    expect(lastX).toBeGreaterThan(0);
    expect(lastX).toBeLessThan(cardWidth);
  });
});
