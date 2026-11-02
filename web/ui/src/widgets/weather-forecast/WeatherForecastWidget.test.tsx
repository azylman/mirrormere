import { useState } from 'react';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as formatModule from '../format';
import { useDisplayStore } from '../../store';
import { WeatherForecastWidget } from './WeatherForecastWidget';
import { WeatherIcon, WeatherIconGlyph } from './WeatherIcon';
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

    const { container: emptyContainer } = render(<WeatherIcon name="" />);
    expect(emptyContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);

    const { container: spacesContainer } = render(<WeatherIcon name="   " size={NaN} />);
    expect(spacesContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);
    expect(spacesContainer.querySelector('svg')?.getAttribute('width')).toBe('20');

    const { container: negContainer } = render(<WeatherIcon name="weather-sunny" size={-5} />);
    expect(negContainer.querySelector('svg')?.getAttribute('width')).toBe('20');

    const { container: zeroContainer } = render(<WeatherIcon name="weather-sunny" size={0} />);
    expect(zeroContainer.querySelector('svg')?.getAttribute('width')).toBe('20');

    const { container: unknownContainer } = render(<WeatherIcon name="unsupported <script>" />);
    expect(unknownContainer.querySelector('svg')?.classList.contains('mm-icon-unsupportedscript')).toBe(true);

    const { container: pureSpecialContainer } = render(<WeatherIcon name="###$$$@@@" />);
    expect(pureSpecialContainer.querySelector('svg')?.classList.contains('mm-icon-weather-cloudy')).toBe(true);
  });

  it('applies custom className and exercises memo cache on prop update', () => {
    const { container, rerender } = render(<WeatherIcon name="weather-sunny" className="custom-test-class" />);
    expect(container.querySelector('svg')?.classList.contains('custom-test-class')).toBe(true);

    rerender(<WeatherIcon name="weather-sunny" className="updated-test-class" />);
    expect(container.querySelector('svg')?.classList.contains('updated-test-class')).toBe(true);
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
    const { container, rerender } = render(<WeatherHourlyGraph hourly={hourly} widgetId="test-w" />);
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
    expect(svg.getAttribute('viewBox')).toBe('0 0 480.0 300.0');

    const labels = Array.from(svg.querySelectorAll('text'));
    const lastHourLabel = labels.find((el) => el.textContent === '7 AM');
    expect(lastHourLabel).toBeDefined();
    const lastX = Number(lastHourLabel?.getAttribute('x'));
    expect(lastX).toBeGreaterThan(0);
    expect(lastX).toBeLessThan(cardWidth);
  });

  it('exercises React compiler memo hit/miss cycles on WeatherHourlyGraph via parent state toggle', () => {
    function GraphParent({ hourly }: { hourly: unknown }) {
      const [count, setCount] = useState(0);
      return (
        <div>
          <button data-testid="rerender-btn" onClick={() => setCount((c) => c + 1)}>
            Increment {count}
          </button>
          <WeatherHourlyGraph hourly={hourly} widgetId="memo-graph" />
        </div>
      );
    }

    const hourly = [
      { time: '2026-10-31T12:00:00Z', temp: 65, precip_prob: 10, icon: 'weather-sunny' },
      { time: '2026-10-31T13:00:00Z', temp: 68, precip_prob: 0, icon: 'weather-partly-cloudy' },
    ];

    const { getByTestId, container } = render(<GraphParent hourly={hourly} />);
    expect(container.querySelector('svg.weather-hourly-graph')).not.toBeNull();

    // Trigger parent rerender with identical hourly reference to hit memo cache branch
    fireEvent.click(getByTestId('rerender-btn'));
    expect(getByTestId('rerender-btn').textContent).toContain('Increment 1');
    expect(container.querySelector('svg.weather-hourly-graph')).not.toBeNull();
  });

  it('renders WeatherHourlyGraph when gridLinesPath, areaPath, curvePath are empty or present with precipitation', () => {
    // 1. All paths present with non-zero precipitation
    const spy1 = vi.spyOn(formatModule, 'computeHourlyGraphLayout').mockReturnValueOnce({
      totalWidth: 500,
      svgHeight: 200,
      baseY: 180,
      isTall: false,
      gradientId: 'grad-full',
      areaPath: 'M 0 180 L 100 100 L 200 180 Z',
      curvePath: 'M 0 100 L 100 100',
      gridLinesPath: 'M 50 0 L 50 180',
      points: [
        {
          x: 50,
          y: 50,
          lineY1: 54,
          precipProb: 40,
          precipY: 12,
          icon: 'weather-rainy',
          iconTranslateX: 40,
          iconY: 14,
          iconScale: 0.8,
          tempRound: 65,
          tempY: 44,
          tempFontSize: 12,
          circleR: 3.5,
          timeLabel: 'Now',
          timeWeight: '800',
          timeColor: '#000',
          timeY: 102,
          timeFontSize: 11,
        },
      ],
    });
    const { container: cFull } = render(<WeatherHourlyGraph hourly={[{ temp: 65 }]} />);
    expect(cFull.querySelector('svg.weather-hourly-graph')).not.toBeNull();
    expect(cFull.textContent).toContain('☂ 40%');
    expect(cFull.querySelectorAll('path').length).toBeGreaterThanOrEqual(3);
    spy1.mockRestore();

    // 2. All paths empty / falsy and precipProb = 0
    const spy2 = vi.spyOn(formatModule, 'computeHourlyGraphLayout').mockReturnValueOnce({
      totalWidth: 500,
      svgHeight: 200,
      baseY: 180,
      isTall: false,
      gradientId: 'grad-empty',
      areaPath: '',
      curvePath: '',
      gridLinesPath: '',
      points: [
        {
          x: 50,
          y: 50,
          lineY1: 54,
          precipProb: 0,
          precipY: 12,
          icon: 'weather-sunny',
          iconTranslateX: 40,
          iconY: 14,
          iconScale: 0.8,
          tempRound: 65,
          tempY: 44,
          tempFontSize: 12,
          circleR: 3.5,
          timeLabel: 'Now',
          timeWeight: '800',
          timeColor: '#000',
          timeY: 102,
          timeFontSize: 11,
        },
      ],
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
    const { container: c1 } = render(<WeatherIcon name="weather-sunny" size={100} className="large-icon" />);
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
    for (const glyph of glyphs) {
      const { container } = render(<WeatherIcon name={glyph} />);
      expect(container.querySelector('svg')).not.toBeNull();
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
});
