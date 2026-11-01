import { cleanup, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useDisplayStore } from '../../store';
import { WeatherForecastWidget } from './WeatherForecastWidget';

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
