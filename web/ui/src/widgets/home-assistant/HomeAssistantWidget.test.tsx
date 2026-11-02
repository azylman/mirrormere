import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../../store';
import { WIDGET_COMPONENTS, widgetComponentFor } from '../registry';
import {
  HomeAssistantWidget,
  dispatchWidgetAction,
  getDomain,
  getDomainIcon,
  getEntityName,
} from './HomeAssistantWidget';

const initialStore = useDisplayStore.getState();

describe('HomeAssistantWidget', () => {
  beforeEach(() => {
    useDisplayStore.setState(initialStore, true);
    vi.restoreAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('registers in WIDGET_COMPONENTS and widgetComponentFor resolves home-assistant', () => {
    expect(WIDGET_COMPONENTS['home-assistant']).toBe(HomeAssistantWidget);
    expect(widgetComponentFor('home-assistant')).toBe(HomeAssistantWidget);
  });

  it('extracts domain, entity name, and icons correctly', () => {
    expect(getDomain('light.kitchen_ceiling')).toBe('light');
    expect(getDomain('switch.ac_plug')).toBe('switch');
    expect(getDomain('climate.hallway')).toBe('climate');
    expect(getDomain('')).toBe('');

    expect(
      getEntityName(
        { entity_id: 'light.kitchen_ceiling', state: 'on', attributes: { friendly_name: 'Kitchen Main Light' } },
        'light.kitchen_ceiling',
      ),
    ).toBe('Kitchen Main Light');

    expect(getEntityName(undefined, 'switch.living_room_fan', 'Custom Fan Name')).toBe('Custom Fan Name');
    expect(getEntityName(undefined, 'fan.ceiling_fan')).toBe('ceiling fan');

    expect(getDomainIcon('light', 'on')).toBe('💡');
    expect(getDomainIcon('light', 'off')).toBe('○');
    expect(getDomainIcon('lock', 'locked')).toBe('🔒');
    expect(getDomainIcon('climate')).toBe('🌡️');
    expect(getDomainIcon('media_player', 'playing')).toBe('🔊');
    expect(getDomainIcon('camera')).toBe('📹');
  });

  it('renders loading state when data is unhydrated ({})', () => {
    useDisplayStore.setState({
      widgets: {
        'ha-1': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {},
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-1" type="home-assistant" />);
    const root = container.querySelector('.widget-home-assistant');
    expect(root).not.toBeNull();
    expect(container.querySelector('.ha-loading-spinner')?.textContent).toContain('Loading Home Assistant...');
  });

  it('renders error state when entry state is error', () => {
    useDisplayStore.setState({
      widgets: {
        'ha-err': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'error',
          data: {},
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-err" type="home-assistant" />);
    const err = container.querySelector('.widget-ha-error');
    expect(err).not.toBeNull();
    expect(err?.textContent).toContain('Unable to load Home Assistant');
  });

  it('renders Lovelace sections and nested cards layout', () => {
    useDisplayStore.setState({
      widgets: {
        'ha-sections': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            title: 'Downstairs Tablet',
            view: {
              title: 'Main Floor',
              type: 'sections',
              sections: [
                {
                  title: 'Living Room',
                  cards: [
                    { type: 'tile', entity: 'light.living_room' },
                    { type: 'button', entity: 'scene.evening_relax' },
                  ],
                },
                {
                  title: 'Climate & Security',
                  cards: [
                    { type: 'thermostat', entity: 'climate.living_room' },
                    { type: 'tile', entity: 'lock.front_door' },
                  ],
                },
              ],
            },
            entities: {
              'light.living_room': { entity_id: 'light.living_room', state: 'on', attributes: { friendly_name: 'Living Room Lights', brightness: 200 } },
              'scene.evening_relax': { entity_id: 'scene.evening_relax', state: 'scening', attributes: { friendly_name: 'Relax Mode' } },
              'climate.living_room': { entity_id: 'climate.living_room', state: 'heat', attributes: { friendly_name: 'Thermostat', current_temperature: 68, temperature: 71 } },
              'lock.front_door': { entity_id: 'lock.front_door', state: 'locked', attributes: { friendly_name: 'Front Door' } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-sections" type="home-assistant" />);
    expect(container.querySelector('.ha-title')?.textContent).toBe('Downstairs Tablet');
    const sections = container.querySelectorAll('.ha-section');
    expect(sections.length).toBe(2);
    expect(sections[0].querySelector('.ha-section-title')?.textContent).toBe('Living Room');
    expect(sections[1].querySelector('.ha-section-title')?.textContent).toBe('Climate & Security');

    expect(container.querySelector('[data-entity-id="light.living_room"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="scene.evening_relax"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="climate.living_room"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="lock.front_door"]')).not.toBeNull();
  });

  it('renders Lovelace entities card and grid card', () => {
    useDisplayStore.setState({
      widgets: {
        'ha-cards': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            title: 'Grid Dashboard',
            cards: [
              {
                type: 'entities',
                title: 'Quick Toggles',
                entities: [
                  'switch.kitchen_fan',
                  { entity: 'cover.garage_door', name: 'Main Garage' },
                ],
              },
              {
                type: 'grid',
                columns: 2,
                cards: [
                  { type: 'tile', entity: 'script.morning_routine' },
                  { type: 'picture-entity', entity: 'camera.driveway', title: 'Driveway Cam' },
                ],
              },
            ],
            entities: {
              'switch.kitchen_fan': { entity_id: 'switch.kitchen_fan', state: 'off', attributes: { friendly_name: 'Kitchen Exhaust' } },
              'cover.garage_door': { entity_id: 'cover.garage_door', state: 'closed', attributes: { friendly_name: 'Garage Door' } },
              'script.morning_routine': { entity_id: 'script.morning_routine', state: 'off', attributes: { friendly_name: 'Morning Script' } },
              'camera.driveway': { entity_id: 'camera.driveway', state: 'idle', attributes: { friendly_name: 'Driveway Camera', entity_picture: '/api/camera_proxy/camera.driveway' } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-cards" type="home-assistant" />);
    expect(container.querySelector('.ha-card-entities')).not.toBeNull();
    expect(container.querySelector('.ha-card-title')?.textContent).toBe('Quick Toggles');
    expect(container.querySelector('.ha-card-grid')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="switch.kitchen_fan"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="cover.garage_door"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="camera.driveway"]')).not.toBeNull();
  });

  it('renders all 12 supported domains with touch-friendly controls and dispatches actions', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok', widget_id: 'ha-all-domains' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-all-domains': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            title: 'All Domains Test',
            cards: [
              {
                type: 'entities',
                title: 'All Domains',
                entities: [
                  'light.living_room',
                  'switch.porch_light',
                  'fan.bedroom_fan',
                  'cover.blinds',
                  'lock.front_door',
                  'climate.main_ac',
                  'media_player.soundbar',
                  'camera.porch_cam',
                  'scene.movie_night',
                  'script.clean_all',
                  'sensor.kitchen_temperature',
                  'binary_sensor.front_motion',
                ],
              },
            ],
            entities: {
              'light.living_room': { entity_id: 'light.living_room', state: 'off', attributes: { friendly_name: 'Living Room Light' } },
              'switch.porch_light': { entity_id: 'switch.porch_light', state: 'off', attributes: { friendly_name: 'Porch Switch' } },
              'fan.bedroom_fan': { entity_id: 'fan.bedroom_fan', state: 'off', attributes: { friendly_name: 'Bedroom Fan' } },
              'cover.blinds': { entity_id: 'cover.blinds', state: 'closed', attributes: { friendly_name: 'Blinds' } },
              'lock.front_door': { entity_id: 'lock.front_door', state: 'locked', attributes: { friendly_name: 'Smart Lock' } },
              'climate.main_ac': { entity_id: 'climate.main_ac', state: 'cool', attributes: { friendly_name: 'Main AC', current_temperature: 75, temperature: 72 } },
              'media_player.soundbar': { entity_id: 'media_player.soundbar', state: 'paused', attributes: { friendly_name: 'Soundbar', media_title: 'Bohemian Rhapsody', media_artist: 'Queen' } },
              'camera.porch_cam': { entity_id: 'camera.porch_cam', state: 'idle', attributes: { friendly_name: 'Porch Camera', entity_picture: '/api/camera_proxy/camera.porch_cam' } },
              'scene.movie_night': { entity_id: 'scene.movie_night', state: 'scening', attributes: { friendly_name: 'Movie Night' } },
              'script.clean_all': { entity_id: 'script.clean_all', state: 'off', attributes: { friendly_name: 'Vacuum Routine' } },
              'sensor.kitchen_temperature': { entity_id: 'sensor.kitchen_temperature', state: '72.4', attributes: { friendly_name: 'Kitchen Temp', unit_of_measurement: '°F' } },
              'binary_sensor.front_motion': { entity_id: 'binary_sensor.front_motion', state: 'on', attributes: { friendly_name: 'Front Motion Sensor', device_class: 'motion' } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-all-domains" type="home-assistant" />);

    // 1. Light: tap toggle
    const lightRow = container.querySelector('[data-entity-id="light.living_room"]');
    expect(lightRow).not.toBeNull();
    const lightBtn = lightRow?.querySelector('[data-action="toggle"]') as HTMLButtonElement;
    expect(lightBtn).not.toBeNull();
    await act(async () => {
      fireEvent.click(lightBtn);
    });
    expect(fetchCalls.length).toBe(1);
    expect(fetchCalls[0].url).toBe('/api/widgets/ha-all-domains/action');
    expect(JSON.parse(fetchCalls[0].options.body as string)).toEqual({
      domain: 'light',
      service: 'toggle',
      entity_id: 'light.living_room',
      service_data: { entity_id: 'light.living_room' },
    });

    // 2. Switch: tap toggle
    const switchRow = container.querySelector('[data-entity-id="switch.porch_light"]');
    const switchBtn = switchRow?.querySelector('[data-action="toggle"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(switchBtn);
    });
    expect(fetchCalls.length).toBe(2);
    expect(JSON.parse(fetchCalls[1].options.body as string)).toEqual({
      domain: 'switch',
      service: 'toggle',
      entity_id: 'switch.porch_light',
      service_data: { entity_id: 'switch.porch_light' },
    });

    // 3. Fan: tap toggle
    const fanRow = container.querySelector('[data-entity-id="fan.bedroom_fan"]');
    const fanBtn = fanRow?.querySelector('[data-action="toggle"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(fanBtn);
    });
    expect(fetchCalls.length).toBe(3);
    expect(JSON.parse(fetchCalls[2].options.body as string)).toEqual({
      domain: 'fan',
      service: 'toggle',
      entity_id: 'fan.bedroom_fan',
      service_data: { entity_id: 'fan.bedroom_fan' },
    });

    // 4. Cover: tap open and close
    const coverRow = container.querySelector('[data-entity-id="cover.blinds"]');
    const coverOpenBtn = coverRow?.querySelector('[data-action="open"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(coverOpenBtn);
    });
    expect(fetchCalls.length).toBe(4);
    expect(JSON.parse(fetchCalls[3].options.body as string)).toEqual({
      domain: 'cover',
      service: 'open_cover',
      entity_id: 'cover.blinds',
      service_data: { entity_id: 'cover.blinds' },
    });

    // 5. Lock: tap toggle-lock (starts locked -> unlocks)
    const lockRow = container.querySelector('[data-entity-id="lock.front_door"]');
    const lockBtn = lockRow?.querySelector('[data-action="toggle-lock"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(lockBtn);
    });
    expect(fetchCalls.length).toBe(5);
    expect(JSON.parse(fetchCalls[4].options.body as string)).toEqual({
      domain: 'lock',
      service: 'unlock',
      entity_id: 'lock.front_door',
      service_data: { entity_id: 'lock.front_door' },
    });

    // 6. Climate: tap temp-up (+)
    const climateRow = container.querySelector('[data-entity-id="climate.main_ac"]');
    const tempUpBtn = climateRow?.querySelector('[data-action="temp-up"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(tempUpBtn);
    });
    expect(fetchCalls.length).toBe(6);
    expect(JSON.parse(fetchCalls[5].options.body as string)).toEqual({
      domain: 'climate',
      service: 'set_temperature',
      entity_id: 'climate.main_ac',
      service_data: { entity_id: 'climate.main_ac', temperature: 73 },
    });

    // 7. Media player: tap play-pause
    const mediaRow = container.querySelector('[data-entity-id="media_player.soundbar"]');
    const playPauseBtn = mediaRow?.querySelector('[data-action="play-pause"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(playPauseBtn);
    });
    expect(fetchCalls.length).toBe(7);
    expect(JSON.parse(fetchCalls[6].options.body as string)).toEqual({
      domain: 'media_player',
      service: 'media_play_pause',
      entity_id: 'media_player.soundbar',
      service_data: { entity_id: 'media_player.soundbar' },
    });

    // 8. Scene: tap activate
    const sceneRow = container.querySelector('[data-entity-id="scene.movie_night"]');
    const sceneBtn = sceneRow?.querySelector('[data-action="activate"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(sceneBtn);
    });
    expect(fetchCalls.length).toBe(8);
    expect(JSON.parse(fetchCalls[7].options.body as string)).toEqual({
      domain: 'scene',
      service: 'turn_on',
      entity_id: 'scene.movie_night',
      service_data: { entity_id: 'scene.movie_night' },
    });

    // 9. Script: tap run
    const scriptRow = container.querySelector('[data-entity-id="script.clean_all"]');
    const scriptBtn = scriptRow?.querySelector('[data-action="run"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(scriptBtn);
    });
    expect(fetchCalls.length).toBe(9);
    expect(JSON.parse(fetchCalls[8].options.body as string)).toEqual({
      domain: 'script',
      service: 'turn_on',
      entity_id: 'script.clean_all',
      service_data: { entity_id: 'script.clean_all' },
    });

    // 10. Sensor: verifies state and unit display
    const sensorRow = container.querySelector('[data-entity-id="sensor.kitchen_temperature"]');
    expect(sensorRow?.querySelector('.ha-sensor-val')?.textContent).toBe('72.4');
    expect(sensorRow?.querySelector('.ha-sensor-unit')?.textContent).toBe('°F');

    // 11. Binary sensor: verifies motion pill and status
    const binaryRow = container.querySelector('[data-entity-id="binary_sensor.front_motion"]');
    expect(binaryRow?.querySelector('.ha-binary-pill')?.textContent).toContain('Motion');

    // 12. Camera: refresh button
    const cameraRow = container.querySelector('[data-entity-id="camera.porch_cam"]');
    const cameraRefreshBtn = cameraRow?.querySelector('[data-action="refresh"]') as HTMLButtonElement;
    expect(cameraRefreshBtn).not.toBeNull();
    await act(async () => {
      fireEvent.click(cameraRefreshBtn);
    });
  });

  it('handles action failure by displaying error alert and reverting optimistic update', async () => {
    const fetchMock = vi.fn().mockImplementation(() => {
      return Promise.resolve(new Response('Upstream Home Assistant timeout', { status: 504 }));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-error-action': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [{ type: 'tile', entity: 'switch.patio_heater' }],
            entities: {
              'switch.patio_heater': { entity_id: 'switch.patio_heater', state: 'off', attributes: { friendly_name: 'Patio Heater' } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-error-action" type="home-assistant" />);
    const toggleBtn = container.querySelector('[data-action="toggle"]') as HTMLButtonElement;
    expect(toggleBtn).not.toBeNull();

    await act(async () => {
      fireEvent.click(toggleBtn);
    });

    await waitFor(() => {
      expect(container.querySelector('.ha-alert-banner')).not.toBeNull();
      expect(container.querySelector('.ha-alert-banner')?.textContent).toContain('Widget action failed (HTTP 504)');
    });
  });

  it('renders fallback entity list when no cards or view are configured', () => {
    useDisplayStore.setState({
      widgets: {
        'ha-fallback': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            entities: {
              'light.study': { entity_id: 'light.study', state: 'on', attributes: { friendly_name: 'Study Light' } },
              'climate.bedroom': { entity_id: 'climate.bedroom', state: 'cool', attributes: { friendly_name: 'Bedroom AC', temperature: 69 } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-fallback" type="home-assistant" />);
    expect(container.querySelector('[data-entity-id="light.study"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="climate.bedroom"]')).not.toBeNull();
  });

  it('dispatches custom service tap_action on button card', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-tap-action': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              {
                type: 'button',
                name: 'Bedtime',
                tap_action: {
                  action: 'call-service',
                  service: 'scene.turn_on',
                  target: { entity_id: 'scene.goodnight' },
                },
              },
            ],
            entities: {},
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-tap-action" type="home-assistant" />);
    const btn = container.querySelector('.ha-card-button') as HTMLButtonElement;
    expect(btn).not.toBeNull();
    await act(async () => {
      fireEvent.click(btn);
    });

    expect(fetchCalls.length).toBe(1);
    expect(JSON.parse(fetchCalls[0].options.body as string)).toEqual({
      domain: 'scene',
      service: 'turn_on',
      entity_id: 'scene.goodnight',
      service_data: { entity_id: 'scene.goodnight' },
    });
  });

  it('dispatchWidgetAction helper functions properly directly', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'ok' })));
    await dispatchWidgetAction(
      'my-widget',
      {
        domain: 'light',
        service: 'turn_on',
        entity_id: 'light.desk',
        service_data: { brightness: 128 },
      },
      fetchMock,
    );

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/widgets/my-widget/action',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          domain: 'light',
          service: 'turn_on',
          entity_id: 'light.desk',
          service_data: { entity_id: 'light.desk', brightness: 128 },
        }),
      }),
    );
  });

  it('handles helper function edge cases (getDomain, getEntityName, getDomainIcon)', () => {
    expect(getDomain('sensor')).toBe('sensor');
    expect(getDomain('')).toBe('');

    expect(getEntityName(undefined, 'nodots')).toBe('nodots');
    expect(getEntityName(undefined, 'switch.bedside_table_lamp')).toBe('bedside table lamp');
    expect(getEntityName(undefined, 'light.bulb', '   ')).toBe('bulb');
    expect(getEntityName(undefined, 'light.bulb', 'Explicit Custom')).toBe('Explicit Custom');
    expect(
      getEntityName(
        { entity_id: 'light.bulb', state: 'on', attributes: { friendly_name: 123 as unknown as string } },
        'light.bulb',
      ),
    ).toBe('bulb');

    expect(getDomainIcon('fan', 'on')).toBe('🌀');
    expect(getDomainIcon('fan', 'off')).toBe('🪭');
    expect(getDomainIcon('cover', 'open')).toBe('🪟');
    expect(getDomainIcon('cover', 'closed')).toBe('🚪');
    expect(getDomainIcon('lock', 'locked')).toBe('🔒');
    expect(getDomainIcon('lock', 'unlocked')).toBe('🔓');
    expect(getDomainIcon('media_player', 'playing')).toBe('🔊');
    expect(getDomainIcon('media_player', 'paused')).toBe('🎵');
    expect(getDomainIcon('binary_sensor', 'on')).toBe('🔔');
    expect(getDomainIcon('binary_sensor', 'off')).toBe('🔕');
    expect(getDomainIcon('switch', 'on')).toBe('🔌');
    expect(getDomainIcon('switch', 'off')).toBe('○');
    expect(getDomainIcon('script')).toBe('⚡');
    expect(getDomainIcon('scene')).toBe('🎬');
    expect(getDomainIcon('sensor')).toBe('📊');
    expect(getDomainIcon('camera')).toBe('📹');
    expect(getDomainIcon('vacuum')).toBe('⚙️');
  });

  it('handles dispatchWidgetAction payload options and error rejections', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'ok' })));

    // 1. Without entity_id, without service_data
    await dispatchWidgetAction('w-1', { domain: 'homeassistant', service: 'restart' }, fetchMock);
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ domain: 'homeassistant', service: 'restart' });

    // 2. Without entity_id, with service_data
    await dispatchWidgetAction(
      'w-1',
      { domain: 'notify', service: 'notify', service_data: { message: 'hello' } },
      fetchMock,
    );
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({
      domain: 'notify',
      service: 'notify',
      service_data: { message: 'hello' },
    });

    // 3. Response not ok, res.text() rejects
    const failMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      text: () => Promise.reject(new Error('fail text')),
    });
    await expect(
      dispatchWidgetAction('w-1', { domain: 'light', service: 'toggle' }, failMock as unknown as typeof fetch),
    ).rejects.toThrow('Widget action failed (HTTP 500): ');

    // 4. Response ok, res.json() rejects
    const jsonFailMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.reject(new Error('bad json')),
    });
    const res = await dispatchWidgetAction(
      'w-1',
      { domain: 'light', service: 'toggle' },
      jsonFailMock as unknown as typeof fetch,
    );
    expect(res).toEqual({});
  });

  it('covers domain optimistic updates and non-Error action rejections with dismiss', async () => {
    let rejectPromise: ((err: unknown) => void) | null = null;
    const fetchMock = vi.fn().mockImplementation(() => {
      return new Promise((_resolve, reject) => {
        rejectPromise = reject;
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-opt': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              { type: 'tile', entity: 'light.desk' },
              { type: 'tile', entity: 'fan.living' },
              { type: 'tile', entity: 'lock.back' },
              { type: 'tile', entity: 'cover.shades' },
              { type: 'thermostat', entity: 'climate.hall' },
              { type: 'tile', entity: 'media_player.tv' },
            ],
            entities: {
              'light.desk': { entity_id: 'light.desk', state: 'on', attributes: { friendly_name: 'Desk Light', brightness: 128 } },
              'fan.living': { entity_id: 'fan.living', state: 'on', attributes: { friendly_name: 'Living Fan', percentage: 66 } },
              'lock.back': { entity_id: 'lock.back', state: 'unlocked', attributes: { friendly_name: 'Back Door' } },
              'cover.shades': { entity_id: 'cover.shades', state: 'open', attributes: { friendly_name: 'Shades' } },
              'climate.hall': { entity_id: 'climate.hall', state: 'cool', attributes: { friendly_name: 'Hall Climate', temperature: 72, current_temperature: 70 } },
              'media_player.tv': { entity_id: 'media_player.tv', state: 'playing', attributes: { friendly_name: 'Living TV' } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-opt" type="home-assistant" />);

    // Toggle desk light (optimistically turns off)
    const deskTile = container.querySelector('[data-entity-id="light.desk"]');
    const deskToggle = deskTile?.querySelector('[data-action="toggle"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(deskToggle);
    });

    // Reject with a plain string
    await act(async () => {
      rejectPromise!('Raw failure string');
    });

    // Alert banner should display string error fallback
    await waitFor(() => {
      const banner = container.querySelector('.ha-alert-banner');
      expect(banner?.textContent).toContain('Action failed');
    });

    // Dismiss banner
    const dismissBtn = container.querySelector('.ha-banner-dismiss') as HTMLButtonElement;
    expect(dismissBtn).not.toBeNull();
    await act(async () => {
      fireEvent.click(dismissBtn);
    });
    expect(container.querySelector('.ha-alert-banner')).toBeNull();
  });

  it('covers cardsToRender fallback paths, view.cards, and header titles', () => {
    // 1. Snapshot with view.cards
    useDisplayStore.setState({
      widgets: {
        'ha-view-cards': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            view: {
              title: 'Floor View',
              cards: [{ type: 'tile', entity: 'switch.coffee' }],
            },
            entities: {
              'switch.coffee': { entity_id: 'switch.coffee', state: 'on', attributes: { friendly_name: 'Coffee Maker' } },
            },
            last_updated: '2026-11-02T09:00:00Z',
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container, unmount } = render(<HomeAssistantWidget id="ha-view-cards" type="home-assistant" />);
    expect(container.querySelector('.ha-title')?.textContent).toBe('Floor View');
    expect(container.querySelector('.ha-last-updated')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="switch.coffee"]')).not.toBeNull();
    unmount();

    // 2. Snapshot with default title ('Home Assistant') and entity domain fallback cards
    useDisplayStore.setState({
      widgets: {
        'ha-fallback-domains': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            entities: {
              'climate.heat': { entity_id: 'climate.heat', state: 'heat', attributes: {} },
              'camera.backyard': { entity_id: 'camera.backyard', state: 'idle', attributes: {} },
              'scene.sunset': { entity_id: 'scene.sunset', state: 'scening', attributes: {} },
              'script.routine': { entity_id: 'script.routine', state: 'off', attributes: {} },
              'sensor.power': { entity_id: 'sensor.power', state: '150', attributes: {} },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container: fallbackContainer } = render(<HomeAssistantWidget id="ha-fallback-domains" type="home-assistant" />);
    expect(fallbackContainer.querySelector('.ha-title')?.textContent).toBe('Home Assistant');
    expect(fallbackContainer.querySelector('.ha-card-thermostat')).not.toBeNull();
    expect(fallbackContainer.querySelector('.ha-card-picture')).not.toBeNull();
    expect(fallbackContainer.querySelectorAll('.ha-card-button').length).toBe(2);
    expect(fallbackContainer.querySelector('.ha-card-tile')).not.toBeNull();
  });

  it('covers CardRenderer grid custom columns, nested card wrapper, and unknown cards', () => {
    useDisplayStore.setState({
      widgets: {
        'ha-custom-cards': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              {
                type: 'grid',
                columns: 3,
                cards: [{ type: 'tile', entity: 'switch.one' }],
              },
              {
                type: 'vertical-stack',
                title: 'Nested Stack',
                cards: [{ type: 'tile', entity: 'switch.two' }],
              },
              {
                type: 'custom:unsupported-card',
                title: 'Unsupported',
              },
            ],
            entities: {
              'switch.one': { entity_id: 'switch.one', state: 'off', attributes: {} },
              'switch.two': { entity_id: 'switch.two', state: 'on', attributes: {} },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-custom-cards" type="home-assistant" />);
    const grid = container.querySelector('.ha-card-grid') as HTMLElement;
    expect(grid?.style.gridTemplateColumns).toContain('repeat(3, minmax(0, 1fr))');
    expect(container.querySelector('.ha-card-wrapper')).not.toBeNull();
    expect(container.querySelector('.ha-card-unknown')?.textContent).toBe('Unsupported');
  });

  it('covers ButtonCard tap actions, inFlight disabling, and default domain behaviors', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-button-tests': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              { type: 'button', entity: 'scene.night_mode' },
              { type: 'button', entity: 'script.run_vac' },
              { type: 'button', entity: 'light.lamp' },
              { type: 'button', entity: 'lock.door_locked' },
              { type: 'button', entity: 'lock.door_unlocked' },
              { type: 'button', entity: 'input_boolean.guest_mode' },
              {
                type: 'button',
                entity: 'light.custom_call',
                tap_action: {
                  action: 'call_service',
                  service: 'toggle',
                  data: { transition: 2 },
                },
              },
            ],
            entities: {
              'scene.night_mode': { entity_id: 'scene.night_mode', state: 'scening', attributes: {} },
              'script.run_vac': { entity_id: 'script.run_vac', state: 'off', attributes: {} },
              'light.lamp': { entity_id: 'light.lamp', state: 'off', attributes: {} },
              'lock.door_locked': { entity_id: 'lock.door_locked', state: 'locked', attributes: {} },
              'lock.door_unlocked': { entity_id: 'lock.door_unlocked', state: 'unlocked', attributes: {} },
              'input_boolean.guest_mode': { entity_id: 'input_boolean.guest_mode', state: 'off', attributes: {} },
              'light.custom_call': { entity_id: 'light.custom_call', state: 'on', attributes: {} },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-button-tests" type="home-assistant" />);
    const buttons = container.querySelectorAll('.ha-card-button');

    // 1. Scene default
    await act(async () => {
      fireEvent.click(buttons[0]);
    });
    expect(JSON.parse(fetchCalls[0].options.body as string)).toEqual({
      domain: 'scene',
      service: 'turn_on',
      entity_id: 'scene.night_mode',
      service_data: { entity_id: 'scene.night_mode' },
    });

    // 2. Script default
    await act(async () => {
      fireEvent.click(buttons[1]);
    });
    expect(JSON.parse(fetchCalls[1].options.body as string)).toEqual({
      domain: 'script',
      service: 'turn_on',
      entity_id: 'script.run_vac',
      service_data: { entity_id: 'script.run_vac' },
    });

    // 3. Light default
    await act(async () => {
      fireEvent.click(buttons[2]);
    });
    expect(JSON.parse(fetchCalls[2].options.body as string)).toEqual({
      domain: 'light',
      service: 'toggle',
      entity_id: 'light.lamp',
      service_data: { entity_id: 'light.lamp' },
    });

    // 4. Lock locked -> unlock
    await act(async () => {
      fireEvent.click(buttons[3]);
    });
    expect(JSON.parse(fetchCalls[3].options.body as string)).toEqual({
      domain: 'lock',
      service: 'unlock',
      entity_id: 'lock.door_locked',
      service_data: { entity_id: 'lock.door_locked' },
    });

    // 5. Lock unlocked -> lock
    await act(async () => {
      fireEvent.click(buttons[4]);
    });
    expect(JSON.parse(fetchCalls[4].options.body as string)).toEqual({
      domain: 'lock',
      service: 'lock',
      entity_id: 'lock.door_unlocked',
      service_data: { entity_id: 'lock.door_unlocked' },
    });

    // 6. Generic input_boolean -> toggle
    await act(async () => {
      fireEvent.click(buttons[5]);
    });
    expect(JSON.parse(fetchCalls[5].options.body as string)).toEqual({
      domain: 'input_boolean',
      service: 'toggle',
      entity_id: 'input_boolean.guest_mode',
      service_data: { entity_id: 'input_boolean.guest_mode' },
    });

    // 7. Custom call_service with data
    await act(async () => {
      fireEvent.click(buttons[6]);
    });
    expect(JSON.parse(fetchCalls[6].options.body as string)).toEqual({
      domain: 'light',
      service: 'toggle',
      entity_id: 'light.custom_call',
      service_data: { entity_id: 'light.custom_call', transition: 2 },
    });
  });

  it('covers ThermostatCard dial buttons, modes, and defaults', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-tstat': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              {
                type: 'thermostat',
                entity: 'climate.hallway',
                title: 'Main Floor Thermostat',
              },
            ],
            entities: {
              'climate.hallway': { entity_id: 'climate.hallway', state: 'heat', attributes: {
                  friendly_name: 'Hallway Climate',
                  current_temperature: 69,
                  temperature: 71,
                  hvac_modes: ['off', 'heat', 'cool'],
                } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-tstat" type="home-assistant" />);
    expect(container.querySelector('.ha-thermostat-title')?.textContent).toBe('Main Floor Thermostat');
    expect(container.querySelector('.ha-temp-val')?.textContent).toBe('69°');

    // Decrease temperature
    const decreaseBtn = container.querySelector('[aria-label="Decrease Temperature"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(decreaseBtn);
    });
    expect(JSON.parse(fetchCalls[0].options.body as string)).toEqual({
      domain: 'climate',
      service: 'set_temperature',
      entity_id: 'climate.hallway',
      service_data: { entity_id: 'climate.hallway', temperature: 70 },
    });

    // Increase temperature (optimistic update previously reduced target from 71 to 70, so 70 + 1 = 71)
    const increaseBtn = container.querySelector('[aria-label="Increase Temperature"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(increaseBtn);
    });
    expect(JSON.parse(fetchCalls[1].options.body as string)).toEqual({
      domain: 'climate',
      service: 'set_temperature',
      entity_id: 'climate.hallway',
      service_data: { entity_id: 'climate.hallway', temperature: 71 },
    });

    // Set mode to cool
    const coolBtn = container.querySelectorAll('.ha-btn-mode')[2] as HTMLButtonElement;
    expect(coolBtn.textContent).toBe('cool');
    await act(async () => {
      fireEvent.click(coolBtn);
    });
    expect(JSON.parse(fetchCalls[2].options.body as string)).toEqual({
      domain: 'climate',
      service: 'set_hvac_mode',
      entity_id: 'climate.hallway',
      service_data: { entity_id: 'climate.hallway', hvac_mode: 'cool' },
    });
  });

  it('covers PictureEntityCard interactions and camera refresh', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-pic': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              {
                type: 'picture-entity',
                entity: 'camera.yard',
                camera_image: '/api/camera/yard.jpg',
              },
              {
                type: 'picture-entity',
                entity: 'switch.garden_fountain',
                image: '/api/static/fountain.jpg',
              },
              { type: 'picture-entity', entity: 'camera.doorbell' },
            ],
            entities: {
              'camera.yard': { entity_id: 'camera.yard', state: 'idle', attributes: {} },
              'switch.garden_fountain': { entity_id: 'switch.garden_fountain', state: 'off', attributes: {} },
              'camera.doorbell': { entity_id: 'camera.doorbell', state: 'idle', attributes: {} },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-pic" type="home-assistant" />);
    const picCards = container.querySelectorAll('.ha-card-picture');

    // 1. Camera tap triggers refreshCamera and updates query timestamp
    await act(async () => {
      fireEvent.click(picCards[0]);
    });
    const yardImg = picCards[0].querySelector('img') as HTMLImageElement;
    expect(yardImg.src).toContain('?ts=');

    // 2. Non-camera picture card tap triggers toggle
    await act(async () => {
      fireEvent.click(picCards[1]);
    });
    expect(JSON.parse(fetchCalls[0].options.body as string)).toEqual({
      domain: 'switch',
      service: 'toggle',
      entity_id: 'switch.garden_fountain',
      service_data: { entity_id: 'switch.garden_fountain' },
    });

    // 3. Camera with no image renders fallback container
    expect(picCards[2].querySelector('.ha-picture-fallback')).not.toBeNull();
  });

  it('covers DomainTouchControls quick brightness, speeds, cover stop/close, lock toggle, climate temp-down, media buttons, binary sensors, and unknown domains', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-subcontrols': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              {
                type: 'entities',
                entities: [
                  'light.active_lamp',
                  'fan.active_fan',
                  'cover.curtain',
                  'lock.unlocked_lock',
                  'climate.bedroom_heat',
                  'media_player.kitchen_speaker',
                  'binary_sensor.door_contact',
                  'binary_sensor.window_contact',
                  'binary_sensor.office_occupancy',
                  'binary_sensor.user_presence',
                  'vacuum.roborock',
                ],
              },
            ],
            entities: {
              'light.active_lamp': { entity_id: 'light.active_lamp', state: 'on', attributes: { friendly_name: 'Lamp', brightness: 128 } },
              'fan.active_fan': { entity_id: 'fan.active_fan', state: 'on', attributes: { friendly_name: 'Fan', percentage: 66 } },
              'cover.curtain': { entity_id: 'cover.curtain', state: 'open', attributes: { friendly_name: 'Curtain' } },
              'lock.unlocked_lock': { entity_id: 'lock.unlocked_lock', state: 'unlocked', attributes: { friendly_name: 'Lock' } },
              'climate.bedroom_heat': { entity_id: 'climate.bedroom_heat', state: 'heat', attributes: { friendly_name: 'Bedroom Heat', temperature: 70 } },
              'media_player.kitchen_speaker': { entity_id: 'media_player.kitchen_speaker', state: 'playing', attributes: { friendly_name: 'Kitchen Speaker' } },
              'binary_sensor.door_contact': { entity_id: 'binary_sensor.door_contact', state: 'off', attributes: { friendly_name: 'Door', device_class: 'door' } },
              'binary_sensor.window_contact': { entity_id: 'binary_sensor.window_contact', state: 'on', attributes: { friendly_name: 'Window', device_class: 'window' } },
              'binary_sensor.office_occupancy': { entity_id: 'binary_sensor.office_occupancy', state: 'off', attributes: { friendly_name: 'Office Occupancy', device_class: 'occupancy' } },
              'binary_sensor.user_presence': { entity_id: 'binary_sensor.user_presence', state: 'on', attributes: { friendly_name: 'Presence', device_class: 'presence' } },
              'vacuum.roborock': { entity_id: 'vacuum.roborock', state: 'docked', attributes: { friendly_name: 'Vacuum' } },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-subcontrols" type="home-assistant" />);

    // 1. Light quick brightness buttons (50% button is active since brightness=128)
    const lampRow = container.querySelector('[data-entity-id="light.active_lamp"]');
    const brightnessBtns = lampRow?.querySelectorAll('.ha-btn-sub') as NodeListOf<HTMLButtonElement>;
    expect(brightnessBtns[1].classList.contains('active')).toBe(true);
    await act(async () => {
      fireEvent.click(brightnessBtns[0]); // 25%
    });
    expect(JSON.parse(fetchCalls[0].options.body as string)).toEqual({
      domain: 'light',
      service: 'turn_on',
      entity_id: 'light.active_lamp',
      service_data: { entity_id: 'light.active_lamp', brightness: 64 },
    });

    // 2. Fan quick speed buttons (Med button is active)
    const fanRow = container.querySelector('[data-entity-id="fan.active_fan"]');
    const speedBtns = fanRow?.querySelectorAll('.ha-btn-sub') as NodeListOf<HTMLButtonElement>;
    expect(speedBtns[1].classList.contains('active')).toBe(true);
    await act(async () => {
      fireEvent.click(speedBtns[0]); // Low (33)
    });
    expect(JSON.parse(fetchCalls[1].options.body as string)).toEqual({
      domain: 'fan',
      service: 'set_percentage',
      entity_id: 'fan.active_fan',
      service_data: { entity_id: 'fan.active_fan', percentage: 33 },
    });
    await act(async () => {
      fireEvent.click(speedBtns[2]); // High (100)
    });
    expect(JSON.parse(fetchCalls[2].options.body as string)).toEqual({
      domain: 'fan',
      service: 'set_percentage',
      entity_id: 'fan.active_fan',
      service_data: { entity_id: 'fan.active_fan', percentage: 100 },
    });

    // 3. Cover stop and close
    const coverRow = container.querySelector('[data-entity-id="cover.curtain"]');
    const stopBtn = coverRow?.querySelector('[data-action="stop"]') as HTMLButtonElement;
    const closeBtn = coverRow?.querySelector('[data-action="close"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(stopBtn);
    });
    expect(JSON.parse(fetchCalls[3].options.body as string)).toEqual({
      domain: 'cover',
      service: 'stop_cover',
      entity_id: 'cover.curtain',
      service_data: { entity_id: 'cover.curtain' },
    });
    await act(async () => {
      fireEvent.click(closeBtn);
    });
    expect(JSON.parse(fetchCalls[4].options.body as string)).toEqual({
      domain: 'cover',
      service: 'close_cover',
      entity_id: 'cover.curtain',
      service_data: { entity_id: 'cover.curtain' },
    });

    // 4. Lock currently unlocked -> clicking locks
    const lockRow = container.querySelector('[data-entity-id="lock.unlocked_lock"]');
    const lockBtn = lockRow?.querySelector('[data-action="toggle-lock"]') as HTMLButtonElement;
    expect(lockBtn.textContent).toBe('Lock');
    await act(async () => {
      fireEvent.click(lockBtn);
    });
    expect(JSON.parse(fetchCalls[5].options.body as string)).toEqual({
      domain: 'lock',
      service: 'lock',
      entity_id: 'lock.unlocked_lock',
      service_data: { entity_id: 'lock.unlocked_lock' },
    });

    // 5. Climate temp down (-)
    const climateRow = container.querySelector('[data-entity-id="climate.bedroom_heat"]');
    const tempDownBtn = climateRow?.querySelector('[data-action="temp-down"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(tempDownBtn);
    });
    expect(JSON.parse(fetchCalls[6].options.body as string)).toEqual({
      domain: 'climate',
      service: 'set_temperature',
      entity_id: 'climate.bedroom_heat',
      service_data: { entity_id: 'climate.bedroom_heat', temperature: 69 },
    });

    // 6. Media player prev, next, vol-down, vol-up
    const mediaRow = container.querySelector('[data-entity-id="media_player.kitchen_speaker"]');
    const prevBtn = mediaRow?.querySelector('[data-action="prev"]') as HTMLButtonElement;
    const nextBtn = mediaRow?.querySelector('[data-action="next"]') as HTMLButtonElement;
    const volDownBtn = mediaRow?.querySelector('[data-action="vol-down"]') as HTMLButtonElement;
    const volUpBtn = mediaRow?.querySelector('[data-action="vol-up"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(prevBtn);
      fireEvent.click(nextBtn);
      fireEvent.click(volDownBtn);
      fireEvent.click(volUpBtn);
    });
    expect(JSON.parse(fetchCalls[7].options.body as string).service).toBe('media_previous_track');
    expect(JSON.parse(fetchCalls[8].options.body as string).service).toBe('media_next_track');
    expect(JSON.parse(fetchCalls[9].options.body as string).service).toBe('volume_down');
    expect(JSON.parse(fetchCalls[10].options.body as string).service).toBe('volume_up');

    // 7. Binary sensor device classes
    expect(container.querySelector('[data-entity-id="binary_sensor.door_contact"]')?.textContent).toContain('Closed');
    expect(container.querySelector('[data-entity-id="binary_sensor.window_contact"]')?.textContent).toContain('Open');
    expect(container.querySelector('[data-entity-id="binary_sensor.office_occupancy"]')?.textContent).toContain('Clear');
    expect(container.querySelector('[data-entity-id="binary_sensor.user_presence"]')?.textContent).toContain('Home');

    // 8. Vacuum generic state
    expect(container.querySelector('[data-entity-id="vacuum.roborock"] .ha-generic-state')?.textContent).toBe('docked');
  });

  it('covers formatStateLabel permutations across multiple domains and state configs', () => {
    useDisplayStore.setState({
      widgets: {
        'ha-labels': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              { type: 'tile', entity: 'light.on_no_brightness' },
              { type: 'tile', entity: 'climate.no_cur_temp' },
              { type: 'tile', entity: 'media_player.title_only' },
              { type: 'tile', entity: 'media_player.no_title' },
              { type: 'tile', entity: 'sensor.no_unit' },
            ],
            entities: {
              'light.on_no_brightness': { entity_id: 'light.on_no_brightness', state: 'on', attributes: {} },
              'climate.no_cur_temp': { entity_id: 'climate.no_cur_temp', state: 'auto', attributes: {} },
              'media_player.title_only': { entity_id: 'media_player.title_only', state: 'playing', attributes: { media_title: 'Single Title' } },
              'media_player.no_title': { entity_id: 'media_player.no_title', state: 'idle', attributes: {} },
              'sensor.no_unit': { entity_id: 'sensor.no_unit', state: 'online', attributes: {} },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-labels" type="home-assistant" />);
    expect(container.querySelector('[data-entity-id="light.on_no_brightness"] .ha-tile-state')?.textContent).toBe('On');
    expect(container.querySelector('[data-entity-id="climate.no_cur_temp"] .ha-tile-state')?.textContent).toBe('auto');
    expect(container.querySelector('[data-entity-id="media_player.title_only"] .ha-tile-state')?.textContent).toBe(
      'Single Title',
    );
    expect(container.querySelector('[data-entity-id="media_player.no_title"] .ha-tile-state')?.textContent).toBe('idle');
    expect(container.querySelector('[data-entity-id="sensor.no_unit"] .ha-tile-state')?.textContent).toBe('online');
  });

  it('covers remaining optimistic action branches and ButtonCard inFlight/svcEnt branches', async () => {
    let resolveCall: (() => void) | null = null;
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return new Promise((resolve) => {
        resolveCall = () => resolve(new Response(JSON.stringify({ status: 'ok' })));
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'ha-deep-branches': {
          meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
          state: 'healthy',
          data: {
            cards: [
              { type: 'tile', entity: 'fan.opt_fan' },
              { type: 'tile', entity: 'light.opt_light' },
              { type: 'tile', entity: 'media_player.opt_player' },
              {
                type: 'button',
                entity: 'light.opt_btn',
                tap_action: {
                  action: 'call-service',
                  service: '.toggle',
                  service_data: { entity_id: 'light.opt_btn' },
                },
              },
              {
                name: 'Unnamed Type Card',
              },
              {
                // completely empty card -> renders 'Card'
              },
            ],
            entities: {
              'fan.opt_fan': { entity_id: 'fan.opt_fan', state: 'off', attributes: {} },
              'light.opt_light': { entity_id: 'light.opt_light', state: 'on', attributes: {} },
              'media_player.opt_player': { entity_id: 'media_player.opt_player', state: 'paused', attributes: {} },
              'light.opt_btn': { entity_id: 'light.opt_btn', state: 'off', attributes: {} },
            },
          },
          timestamp: '2026-11-02T08:00:00Z',
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-deep-branches" type="home-assistant" />);

    // Check unknown card fallbacks
    const unknownCards = container.querySelectorAll('.ha-card-unknown');
    expect(unknownCards.length).toBe(2);
    expect(unknownCards[0].textContent).toBe('Unnamed Type Card');
    expect(unknownCards[1].textContent).toBe('Card');

    // 1. ButtonCard tap with svcEnt and empty domain prefix (falls back to domain)
    const btn = container.querySelector('.ha-card-button') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(btn);
    });

    // While inFlight is true, clicking again triggers if (inFlight) return
    await act(async () => {
      fireEvent.click(btn);
    });
    expect(fetchCalls.length).toBe(1);
    expect(JSON.parse(fetchCalls[0].options.body as string)).toEqual({
      domain: 'light',
      service: 'toggle',
      entity_id: 'light.opt_btn',
      service_data: { entity_id: 'light.opt_btn' },
    });

    // Resolve the in-flight request
    await act(async () => {
      resolveCall?.();
    });

    // 2. Play pause when paused -> optimistic playing
    const playPauseBtn = container.querySelector('[data-entity-id="media_player.opt_player"] [data-action="play-pause"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(playPauseBtn);
    });
    await act(async () => {
      resolveCall?.();
    });
    expect(container.querySelector('[data-entity-id="media_player.opt_player"]')?.getAttribute('data-state')).toBe('playing');

    // 3. Toggle light when on -> optimistic turn_off
    const lightToggle = container.querySelector('[data-entity-id="light.opt_light"] [data-action="toggle"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(lightToggle);
    });
    await act(async () => {
      resolveCall?.();
    });
    expect(container.querySelector('[data-entity-id="light.opt_light"]')?.getAttribute('data-state')).toBe('off');
  });
});
