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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [4, 2],
            config: {},
            assets_base: '',
          },
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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [4, 2],
            config: {},
            assets_base: '',
          },
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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [4, 4],
            config: {},
            assets_base: '',
          },
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
              'light.living_room': {
                entity_id: 'light.living_room',
                state: 'on',
                attributes: { friendly_name: 'Living Room Lights', brightness: 200 },
              },
              'scene.evening_relax': {
                entity_id: 'scene.evening_relax',
                state: 'scening',
                attributes: { friendly_name: 'Relax Mode' },
              },
              'climate.living_room': {
                entity_id: 'climate.living_room',
                state: 'heat',
                attributes: { friendly_name: 'Thermostat', current_temperature: 68, temperature: 71 },
              },
              'lock.front_door': {
                entity_id: 'lock.front_door',
                state: 'locked',
                attributes: { friendly_name: 'Front Door' },
              },
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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [4, 2],
            config: {},
            assets_base: '',
          },
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
              'switch.kitchen_fan': {
                entity_id: 'switch.kitchen_fan',
                state: 'off',
                attributes: { friendly_name: 'Kitchen Exhaust' },
              },
              'cover.garage_door': {
                entity_id: 'cover.garage_door',
                state: 'closed',
                attributes: { friendly_name: 'Garage Door' },
              },
              'script.morning_routine': {
                entity_id: 'script.morning_routine',
                state: 'off',
                attributes: { friendly_name: 'Morning Script' },
              },
              'camera.driveway': {
                entity_id: 'camera.driveway',
                state: 'idle',
                attributes: { friendly_name: 'Driveway Camera', entity_picture: '/api/camera_proxy/camera.driveway' },
              },
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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [6, 4],
            config: {},
            assets_base: '',
          },
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
              'light.living_room': {
                entity_id: 'light.living_room',
                state: 'off',
                attributes: { friendly_name: 'Living Room Light' },
              },
              'switch.porch_light': {
                entity_id: 'switch.porch_light',
                state: 'off',
                attributes: { friendly_name: 'Porch Switch' },
              },
              'fan.bedroom_fan': {
                entity_id: 'fan.bedroom_fan',
                state: 'off',
                attributes: { friendly_name: 'Bedroom Fan' },
              },
              'cover.blinds': {
                entity_id: 'cover.blinds',
                state: 'closed',
                attributes: { friendly_name: 'Blinds' },
              },
              'lock.front_door': {
                entity_id: 'lock.front_door',
                state: 'locked',
                attributes: { friendly_name: 'Smart Lock' },
              },
              'climate.main_ac': {
                entity_id: 'climate.main_ac',
                state: 'cool',
                attributes: { friendly_name: 'Main AC', current_temperature: 75, temperature: 72 },
              },
              'media_player.soundbar': {
                entity_id: 'media_player.soundbar',
                state: 'paused',
                attributes: { friendly_name: 'Soundbar', media_title: 'Bohemian Rhapsody', media_artist: 'Queen' },
              },
              'camera.porch_cam': {
                entity_id: 'camera.porch_cam',
                state: 'idle',
                attributes: { friendly_name: 'Porch Camera', entity_picture: '/api/camera_proxy/camera.porch_cam' },
              },
              'scene.movie_night': {
                entity_id: 'scene.movie_night',
                state: 'scening',
                attributes: { friendly_name: 'Movie Night' },
              },
              'script.clean_all': {
                entity_id: 'script.clean_all',
                state: 'off',
                attributes: { friendly_name: 'Vacuum Routine' },
              },
              'sensor.kitchen_temperature': {
                entity_id: 'sensor.kitchen_temperature',
                state: '72.4',
                attributes: { friendly_name: 'Kitchen Temp', unit_of_measurement: '°F' },
              },
              'binary_sensor.front_motion': {
                entity_id: 'binary_sensor.front_motion',
                state: 'on',
                attributes: { friendly_name: 'Front Motion Sensor', device_class: 'motion' },
              },
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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [4, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {
            cards: [{ type: 'tile', entity: 'switch.patio_heater' }],
            entities: {
              'switch.patio_heater': {
                entity_id: 'switch.patio_heater',
                state: 'off',
                attributes: { friendly_name: 'Patio Heater' },
              },
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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [4, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {
            entities: {
              'light.study': {
                entity_id: 'light.study',
                state: 'on',
                attributes: { friendly_name: 'Study Light' },
              },
              'climate.bedroom': {
                entity_id: 'climate.bedroom',
                state: 'cool',
                attributes: { friendly_name: 'Bedroom AC', temperature: 69 },
              },
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
          meta: {
            type: 'home-assistant',
            origin: [0, 0],
            dimensions: [4, 2],
            config: {},
            assets_base: '',
          },
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
});
