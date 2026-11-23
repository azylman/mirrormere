import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../../store';
import { WIDGET_COMPONENTS, widgetComponentFor } from '../registry';
import {
  HomeAssistantWidget,
  applyOptimisticUpdate,
  dispatchWidgetAction,
  formatStateLabel,
  getBinaryLabel,
  getDomain,
  getDomainIcon,
  getEntityName,
} from './HomeAssistantWidget';
import { itemAt } from '../../test-utils/lookup';

function setHaWidget(id: string, data: Record<string, unknown>, state = 'healthy') {
  useDisplayStore.setState({
    widgets: {
      [id]: {
        meta: { type: 'home-assistant', origin: [0, 0], dimensions: [4, 4], config: {}, assets_base: '' },
        state,
        data,
        timestamp: '2026-11-02T08:00:00Z',
      },
    },
  });
}

describe('HomeAssistantWidget', () => {

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
    setHaWidget('ha-1', {});

    const { container } = render(<HomeAssistantWidget id="ha-1" type="home-assistant" />);
    const root = container.querySelector('.widget-home-assistant');
    expect(root).not.toBeNull();
    expect(container.querySelector('.ha-loading-spinner')?.textContent).toContain('Loading Home Assistant...');
  });

  it('renders error state when entry state is error', () => {
    setHaWidget('ha-err', {}, 'error');

    const { container } = render(<HomeAssistantWidget id="ha-err" type="home-assistant" />);
    const err = container.querySelector('.widget-ha-error');
    expect(err).not.toBeNull();
    expect(err?.textContent).toContain('Unable to load Home Assistant');
  });

  it('renders Lovelace sections and nested cards layout', () => {
    setHaWidget('ha-sections', {
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
          });

    const { container } = render(<HomeAssistantWidget id="ha-sections" type="home-assistant" />);
    expect(container.querySelector('.ha-title')?.textContent).toBe('Downstairs Tablet');
    const sections = container.querySelectorAll('.ha-section');
    expect(sections.length).toBe(2);
    expect(itemAt(sections, 0).querySelector('.ha-section-title')?.textContent).toBe('Living Room');
    expect(itemAt(sections, 1).querySelector('.ha-section-title')?.textContent).toBe('Climate & Security');

    expect(container.querySelector('[data-entity-id="light.living_room"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="scene.evening_relax"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="climate.living_room"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="lock.front_door"]')).not.toBeNull();
  });

  it('renders Lovelace entities card and grid card', () => {
    setHaWidget('ha-cards', {
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
          });

    const { container } = render(<HomeAssistantWidget id="ha-cards" type="home-assistant" />);
    expect(container.querySelector('.ha-card-entities')).not.toBeNull();
    expect(container.querySelector('.ha-card-title')?.textContent).toBe('Quick Toggles');
    expect(container.querySelector('.ha-card-grid')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="switch.kitchen_fan"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="cover.garage_door"]')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="camera.driveway"]')).not.toBeNull();
  });

  interface DomainActionCase {
    domain: string;
    action: string;
    entityId: string;
    entity: { state: string; attributes: Record<string, unknown> };
    selector: string;
    subIndex?: number;
    checkActiveSubIndex?: number;
    wantService?: string;
    wantData?: Record<string, unknown>;
    expectedBtnText?: string;
  }

  const domainActionCases: DomainActionCase[] = [
    {
      domain: 'light',
      action: 'toggle off to on',
      entityId: 'light.living_room',
      entity: { state: 'off', attributes: { friendly_name: 'Living Room Light' } },
      selector: '[data-action="toggle"]',
      wantService: 'toggle',
    },
    {
      domain: 'light',
      action: 'quick brightness 25%',
      entityId: 'light.active_lamp',
      entity: { state: 'on', attributes: { friendly_name: 'Lamp', brightness: 128 } },
      selector: '.ha-btn-sub',
      subIndex: 0,
      checkActiveSubIndex: 1,
      wantService: 'turn_on',
      wantData: { brightness: 64 },
    },
    {
      domain: 'switch',
      action: 'toggle',
      entityId: 'switch.porch_light',
      entity: { state: 'off', attributes: { friendly_name: 'Porch Switch' } },
      selector: '[data-action="toggle"]',
      wantService: 'toggle',
    },
    {
      domain: 'fan',
      action: 'toggle',
      entityId: 'fan.bedroom_fan',
      entity: { state: 'off', attributes: { friendly_name: 'Bedroom Fan' } },
      selector: '[data-action="toggle"]',
      wantService: 'toggle',
    },
    {
      domain: 'fan',
      action: 'quick speed Low (33%)',
      entityId: 'fan.active_fan',
      entity: { state: 'on', attributes: { friendly_name: 'Fan', percentage: 66 } },
      selector: '.ha-btn-sub',
      subIndex: 0,
      checkActiveSubIndex: 1,
      wantService: 'set_percentage',
      wantData: { percentage: 33 },
    },
    {
      domain: 'fan',
      action: 'quick speed High (100%)',
      entityId: 'fan.active_fan',
      entity: { state: 'on', attributes: { friendly_name: 'Fan', percentage: 66 } },
      selector: '.ha-btn-sub',
      subIndex: 2,
      wantService: 'set_percentage',
      wantData: { percentage: 100 },
    },
    {
      domain: 'cover',
      action: 'open',
      entityId: 'cover.blinds',
      entity: { state: 'closed', attributes: { friendly_name: 'Blinds' } },
      selector: '[data-action="open"]',
      wantService: 'open_cover',
    },
    {
      domain: 'cover',
      action: 'stop',
      entityId: 'cover.curtain',
      entity: { state: 'open', attributes: { friendly_name: 'Curtain' } },
      selector: '[data-action="stop"]',
      wantService: 'stop_cover',
    },
    {
      domain: 'cover',
      action: 'close',
      entityId: 'cover.curtain',
      entity: { state: 'open', attributes: { friendly_name: 'Curtain' } },
      selector: '[data-action="close"]',
      wantService: 'close_cover',
    },
    {
      domain: 'lock',
      action: 'unlock (when locked)',
      entityId: 'lock.front_door',
      entity: { state: 'locked', attributes: { friendly_name: 'Smart Lock' } },
      selector: '[data-action="toggle-lock"]',
      wantService: 'unlock',
      expectedBtnText: 'Unlock',
    },
    {
      domain: 'lock',
      action: 'lock (when unlocked)',
      entityId: 'lock.unlocked_lock',
      entity: { state: 'unlocked', attributes: { friendly_name: 'Lock' } },
      selector: '[data-action="toggle-lock"]',
      wantService: 'lock',
      expectedBtnText: 'Lock',
    },
    {
      domain: 'climate',
      action: 'temp-up (+)',
      entityId: 'climate.main_ac',
      entity: { state: 'cool', attributes: { friendly_name: 'Main AC', current_temperature: 75, temperature: 72 } },
      selector: '[data-action="temp-up"]',
      wantService: 'set_temperature',
      wantData: { temperature: 73 },
    },
    {
      domain: 'climate',
      action: 'temp-down (-)',
      entityId: 'climate.bedroom_heat',
      entity: { state: 'heat', attributes: { friendly_name: 'Bedroom Heat', temperature: 70 } },
      selector: '[data-action="temp-down"]',
      wantService: 'set_temperature',
      wantData: { temperature: 69 },
    },
    {
      domain: 'media_player',
      action: 'play-pause',
      entityId: 'media_player.soundbar',
      entity: { state: 'paused', attributes: { friendly_name: 'Soundbar' } },
      selector: '[data-action="play-pause"]',
      wantService: 'media_play_pause',
    },
    {
      domain: 'media_player',
      action: 'prev track',
      entityId: 'media_player.kitchen_speaker',
      entity: { state: 'playing', attributes: { friendly_name: 'Kitchen Speaker' } },
      selector: '[data-action="prev"]',
      wantService: 'media_previous_track',
    },
    {
      domain: 'media_player',
      action: 'next track',
      entityId: 'media_player.kitchen_speaker',
      entity: { state: 'playing', attributes: { friendly_name: 'Kitchen Speaker' } },
      selector: '[data-action="next"]',
      wantService: 'media_next_track',
    },
    {
      domain: 'media_player',
      action: 'volume down',
      entityId: 'media_player.kitchen_speaker',
      entity: { state: 'playing', attributes: { friendly_name: 'Kitchen Speaker' } },
      selector: '[data-action="vol-down"]',
      wantService: 'volume_down',
    },
    {
      domain: 'media_player',
      action: 'volume up',
      entityId: 'media_player.kitchen_speaker',
      entity: { state: 'playing', attributes: { friendly_name: 'Kitchen Speaker' } },
      selector: '[data-action="vol-up"]',
      wantService: 'volume_up',
    },
    {
      domain: 'scene',
      action: 'activate',
      entityId: 'scene.movie_night',
      entity: { state: 'scening', attributes: { friendly_name: 'Movie Night' } },
      selector: '[data-action="activate"]',
      wantService: 'turn_on',
    },
    {
      domain: 'script',
      action: 'run',
      entityId: 'script.clean_all',
      entity: { state: 'off', attributes: { friendly_name: 'Vacuum Routine' } },
      selector: '[data-action="run"]',
      wantService: 'turn_on',
    },
    {
      domain: 'camera',
      action: 'refresh',
      entityId: 'camera.porch_cam',
      entity: { state: 'idle', attributes: { friendly_name: 'Porch Camera', entity_picture: '/api/camera_proxy/camera.porch_cam' } },
      selector: '[data-action="refresh"]',
      expectedBtnText: 'Refresh',
    },
  ];

  it.each(domainActionCases)('dispatches domain entity action: $domain ($action)', async (tc) => {
    let callBody: unknown;
    vi.stubGlobal('fetch', vi.fn().mockImplementation((_url: string, options: RequestInit) => {
      callBody = JSON.parse(options.body as string);
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok', widget_id: 'ha-action-test' })));
    }));

    setHaWidget('ha-action-test', {
      cards: [
        { type: 'entities', entities: [tc.entityId] },
        ...(tc.domain === 'camera' ? [{ type: 'picture-entity', entity: tc.entityId }] : []),
      ],
      entities: {
        [tc.entityId]: {
          entity_id: tc.entityId,
          state: tc.entity.state,
          attributes: tc.entity.attributes,
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-action-test" type="home-assistant" />);
    const row = container.querySelector(`[data-entity-id="${tc.entityId}"]`);
    expect(row).not.toBeNull();

    if (tc.checkActiveSubIndex !== undefined) {
      const subs = row?.querySelectorAll('.ha-btn-sub');
      expect(subs?.[tc.checkActiveSubIndex]?.classList.contains('active')).toBe(true);
    }

    let btn: HTMLButtonElement;
    if (tc.subIndex !== undefined) {
      btn = row?.querySelectorAll('.ha-btn-sub')[tc.subIndex] as HTMLButtonElement;
    } else {
      btn = row?.querySelector(tc.selector) as HTMLButtonElement;
    }
    expect(btn).not.toBeNull();

    if (tc.expectedBtnText !== undefined) {
      expect(btn.textContent).toBe(tc.expectedBtnText);
    }

    if (tc.domain === 'camera') {
      const picImg = container.querySelector('.ha-card-picture img') as HTMLImageElement;
      expect(picImg.src).not.toContain('?ts=');
    }

    await act(async () => {
      fireEvent.click(btn);
    });

    if (tc.wantService) {
      expect(callBody).toEqual({
        domain: tc.domain,
        service: tc.wantService,
        entity_id: tc.entityId,
        service_data: { entity_id: tc.entityId, ...(tc.wantData ?? {}) },
      });
    } else {
      expect(callBody).toBeUndefined();
      if (tc.domain === 'camera') {
        const picImg = container.querySelector('.ha-card-picture img') as HTMLImageElement;
        expect(picImg.src).toContain('/api/camera_proxy/camera.porch_cam?ts=');
      }
    }
  });

  interface StateRenderingCase {
    name: string;
    entityId: string;
    rawId?: string;
    state: string;
    attrs: Record<string, unknown>;
    selector: string;
    wantText: string;
  }

  const stateRenderingCases: StateRenderingCase[] = [
    { name: 'sensor value', entityId: 'sensor.kitchen_temperature', state: '72.4', attrs: { unit_of_measurement: '°F' }, selector: '.ha-sensor-val', wantText: '72.4' },
    { name: 'sensor unit', entityId: 'sensor.kitchen_temperature', rawId: 'sensor.kitchen_temperature', state: '72.4', attrs: { unit_of_measurement: '°F' }, selector: '.ha-sensor-unit', wantText: '°F' },
    { name: 'binary sensor motion', entityId: 'binary_sensor.front_motion', state: 'on', attrs: { device_class: 'motion' }, selector: '.ha-binary-pill', wantText: 'Motion' },
    { name: 'binary sensor door', entityId: 'binary_sensor.door_contact', state: 'off', attrs: { device_class: 'door' }, selector: '', wantText: 'Closed' },
    { name: 'binary sensor window', entityId: 'binary_sensor.window_contact', state: 'on', attrs: { device_class: 'window' }, selector: '', wantText: 'Open' },
    { name: 'binary sensor occupancy', entityId: 'binary_sensor.office_occupancy', state: 'off', attrs: { device_class: 'occupancy' }, selector: '', wantText: 'Clear' },
    { name: 'binary sensor presence', entityId: 'binary_sensor.user_presence', state: 'on', attrs: { device_class: 'presence' }, selector: '', wantText: 'Home' },
    { name: 'binary sensor smoke', entityId: 'binary_sensor.smoke', state: 'off', attrs: { device_class: 'smoke' }, selector: '', wantText: 'Off' },
    { name: 'vacuum generic state', entityId: 'vacuum.roborock', state: 'docked', attrs: {}, selector: '.ha-generic-state', wantText: 'docked' },
    { name: 'climate inline temp fallback', entityId: 'climate.no_target', state: 'auto', attrs: {}, selector: '.ha-temp-inline', wantText: '70°' },
  ];

  it.each(stateRenderingCases)('renders read-only entity state: $name ($entityId)', (tc) => {
    const rawId = tc.rawId ?? tc.entityId;
    setHaWidget('ha-state-test', {
      cards: [{ type: 'entities', entities: [rawId] }],
      entities: {
        [rawId]: {
          entity_id: rawId,
          state: tc.state,
          attributes: tc.attrs,
        },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-state-test" type="home-assistant" />);
    const row = container.querySelector(`[data-entity-id="${rawId}"]`);
    expect(row).not.toBeNull();
    const target = tc.selector ? row?.querySelector(tc.selector) : row;
    expect(target?.textContent).toContain(tc.wantText);
  });

  it('renders multiple domains in a single entities card and interacts with controls', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok', widget_id: 'ha-multi' })));
    }));

    setHaWidget('ha-multi', {
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
        {
          type: 'picture-entity',
          entity: 'camera.porch_cam',
        },
      ],
      entities: {
        'light.living_room': { entity_id: 'light.living_room', state: 'off', attributes: { friendly_name: 'Living Room Light' } },
        'switch.porch_light': { entity_id: 'switch.porch_light', state: 'off', attributes: { friendly_name: 'Porch Switch' } },
        'fan.bedroom_fan': { entity_id: 'fan.bedroom_fan', state: 'off', attributes: { friendly_name: 'Bedroom Fan' } },
        'cover.blinds': { entity_id: 'cover.blinds', state: 'closed', attributes: { friendly_name: 'Blinds' } },
        'lock.front_door': { entity_id: 'lock.front_door', state: 'locked', attributes: { friendly_name: 'Smart Lock' } },
        'climate.main_ac': { entity_id: 'climate.main_ac', state: 'cool', attributes: { friendly_name: 'Main AC', current_temperature: 75, temperature: 72 } },
        'media_player.soundbar': { entity_id: 'media_player.soundbar', state: 'paused', attributes: { friendly_name: 'Soundbar' } },
        'camera.porch_cam': { entity_id: 'camera.porch_cam', state: 'idle', attributes: { friendly_name: 'Porch Camera', entity_picture: '/api/camera_proxy/camera.porch_cam' } },
        'scene.movie_night': { entity_id: 'scene.movie_night', state: 'scening', attributes: { friendly_name: 'Movie Night' } },
        'script.clean_all': { entity_id: 'script.clean_all', state: 'off', attributes: { friendly_name: 'Vacuum Routine' } },
        'sensor.kitchen_temperature': { entity_id: 'sensor.kitchen_temperature', state: '72.4', attributes: { friendly_name: 'Kitchen Temp', unit_of_measurement: '°F' } },
        'binary_sensor.front_motion': { entity_id: 'binary_sensor.front_motion', state: 'on', attributes: { friendly_name: 'Front Motion Sensor', device_class: 'motion' } },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-multi" type="home-assistant" />);
    expect(container.querySelectorAll('.ha-entity-row')).toHaveLength(12);
    expect(container.querySelector('[data-entity-id="sensor.kitchen_temperature"] .ha-sensor-val')?.textContent).toBe('72.4');
    expect(container.querySelector('[data-entity-id="sensor.kitchen_temperature"] .ha-sensor-unit')?.textContent).toBe('°F');
    expect(container.querySelector('[data-entity-id="binary_sensor.front_motion"] .ha-binary-pill')?.textContent).toContain('Motion');

    // Toggle light in the multi-entity card
    const lightBtn = container.querySelector('[data-entity-id="light.living_room"] [data-action="toggle"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(lightBtn);
    });
    expect(fetchCalls).toHaveLength(1);
    expect(JSON.parse(itemAt(fetchCalls, 0).options.body as string)).toEqual({
      domain: 'light',
      service: 'toggle',
      entity_id: 'light.living_room',
      service_data: { entity_id: 'light.living_room' },
    });

    // Refresh camera in the multi-entity card and assert picture entity image timestamp update
    const picImgBefore = container.querySelector('.ha-card-picture img') as HTMLImageElement;
    expect(picImgBefore.src).not.toContain('?ts=');
    const cameraRefreshBtn = container.querySelector('[data-entity-id="camera.porch_cam"] [data-action="refresh"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(cameraRefreshBtn);
    });
    const picImgAfter = container.querySelector('.ha-card-picture img') as HTMLImageElement;
    expect(picImgAfter.src).toContain('/api/camera_proxy/camera.porch_cam?ts=');
  });

  it('updates entity state optimistically on action dispatch', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})));
    setHaWidget('ha-optimistic-toggles', {
      cards: [{ type: 'entities', entities: ['media_player.paused_speaker', 'light.active_lamp'] }],
      entities: {
        'media_player.paused_speaker': { entity_id: 'media_player.paused_speaker', state: 'paused', attributes: {} },
        'light.active_lamp': { entity_id: 'light.active_lamp', state: 'on', attributes: {} },
      },
    });
    const { container } = render(<HomeAssistantWidget id="ha-optimistic-toggles" type="home-assistant" />);
    await act(async () => {
      fireEvent.click(container.querySelector('[data-entity-id="media_player.paused_speaker"] [data-action="play-pause"]') as HTMLButtonElement);
    });
    expect(container.querySelector('[data-entity-id="media_player.paused_speaker"]')?.getAttribute('data-state')).toBe('playing');
    await act(async () => {
      fireEvent.click(container.querySelector('[data-entity-id="light.active_lamp"] [data-action="toggle"]') as HTMLButtonElement);
    });
    expect(container.querySelector('[data-entity-id="light.active_lamp"]')?.getAttribute('data-state')).toBe('off');
  });

  it('handles action failure by displaying error alert and reverting optimistic update', async () => {
    const fetchMock = vi.fn().mockImplementation(() => {
      return Promise.resolve(new Response('Upstream Home Assistant timeout', { status: 504 }));
    });
    vi.stubGlobal('fetch', fetchMock);

    setHaWidget('ha-error-action', {
            cards: [{ type: 'tile', entity: 'switch.patio_heater' }],
            entities: {
              'switch.patio_heater': { entity_id: 'switch.patio_heater', state: 'off', attributes: { friendly_name: 'Patio Heater' } },
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
    setHaWidget('ha-fallback', {
            entities: {
              'light.study': { entity_id: 'light.study', state: 'on', attributes: { friendly_name: 'Study Light' } },
              'climate.bedroom': { entity_id: 'climate.bedroom', state: 'cool', attributes: { friendly_name: 'Bedroom AC', temperature: 69 } },
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

    setHaWidget('ha-tap-action', {
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
          });

    const { container } = render(<HomeAssistantWidget id="ha-tap-action" type="home-assistant" />);
    const btn = container.querySelector('.ha-card-button') as HTMLButtonElement;
    expect(btn).not.toBeNull();
    expect(btn.getAttribute('data-entity-id')).toBe('scene.goodnight');
    expect(btn.getAttribute('data-domain')).toBe('scene');
    expect(btn.querySelector('.ha-btn-card-name')?.textContent).toBe('Bedtime');
    await act(async () => {
      fireEvent.click(btn);
    });

    expect(fetchCalls.length).toBe(1);
    expect(JSON.parse(itemAt(fetchCalls, 0).options.body as string)).toEqual({
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
    expect(getEntityName(undefined, '', 'Button Custom')).toBe('Button Custom');
    expect(getEntityName(undefined, 'light.bulb', '   ')).toBe('bulb');
    expect(getEntityName(undefined, 'light.bulb', 'Explicit Custom')).toBe('Explicit Custom');
    expect(
      getEntityName(
        { entity_id: 'light.bulb', state: 'on', attributes: { friendly_name: 123 } },
        'light.bulb',
      ),
    ).toBe('bulb');
    expect(getEntityName({ entity_id: 'light.bulb', state: 'on', attributes: null }, 'light.bulb')).toBe('bulb');

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
    expect(getDomain('LIGHT.BEDROOM')).toBe('light');
  });

  it('handles dispatchWidgetAction payload options and error rejections', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'ok' })));

    // 1. Without entity_id, without service_data
    await dispatchWidgetAction('w-1', { domain: 'homeassistant', service: 'restart' }, fetchMock);
    expect(JSON.parse(itemAt(fetchMock.mock.calls, 0)[1].body)).toEqual({ domain: 'homeassistant', service: 'restart' });

    // 2. Without entity_id, with service_data
    await dispatchWidgetAction(
      'w-1',
      { domain: 'notify', service: 'notify', service_data: { message: 'hello' } },
      fetchMock,
    );
    expect(JSON.parse(itemAt(fetchMock.mock.calls, 1)[1].body)).toEqual({
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
      jsonFailMock,
    );
    expect(res).toEqual({});
    // 5. Default fetchImpl falls back to the global fetch
    const globalFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', globalFetch);
    await dispatchWidgetAction('w-def', { domain: 'light', service: 'toggle' });
    expect(globalFetch).toHaveBeenCalledWith('/api/widgets/w-def/action', expect.objectContaining({ method: 'POST' }));
  });

  it('covers domain optimistic updates and non-Error action rejections with dismiss', async () => {
    let rejectPromise: ((err: unknown) => void) | null = null;
    const fetchMock = vi.fn().mockImplementation(() => {
      return new Promise((_resolve, reject) => {
        rejectPromise = reject;
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    setHaWidget('ha-opt', {
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
    setHaWidget('ha-view-cards', {
            view: {
              title: 'Floor View',
              cards: [{ type: 'tile', entity: 'switch.coffee' }],
            },
            entities: {
              'switch.coffee': { entity_id: 'switch.coffee', state: 'on', attributes: { friendly_name: 'Coffee Maker' } },
            },
            last_updated: '2026-11-02T09:00:00Z',
          });

    const { container, unmount } = render(<HomeAssistantWidget id="ha-view-cards" type="home-assistant" />);
    expect(container.querySelector('.ha-title')?.textContent).toBe('Floor View');
    expect(container.querySelector('.ha-last-updated')).not.toBeNull();
    expect(container.querySelector('[data-entity-id="switch.coffee"]')).not.toBeNull();
    unmount();

    // 2. Snapshot with default title ('Home Assistant') and entity domain fallback cards
    setHaWidget('ha-fallback-domains', {
            entities: {
              'climate.heat': { entity_id: 'climate.heat', state: 'heat', attributes: {} },
              'camera.backyard': { entity_id: 'camera.backyard', state: 'idle', attributes: {} },
              'scene.sunset': { entity_id: 'scene.sunset', state: 'scening', attributes: {} },
              'script.routine': { entity_id: 'script.routine', state: 'off', attributes: {} },
              'sensor.power': { entity_id: 'sensor.power', state: '150', attributes: {} },
            },
          });

    const { container: fallbackContainer } = render(<HomeAssistantWidget id="ha-fallback-domains" type="home-assistant" />);
    expect(fallbackContainer.querySelector('.ha-title')?.textContent).toBe('Home Assistant');
    expect(fallbackContainer.querySelector('.ha-card-thermostat')).not.toBeNull();
    expect(fallbackContainer.querySelector('.ha-card-picture')).not.toBeNull();
    expect(fallbackContainer.querySelectorAll('.ha-card-button').length).toBe(2);
    expect(fallbackContainer.querySelector('.ha-card-tile')).not.toBeNull();
  });

  it('renders grid columns, nested wrappers, section edges and unknown card labels', () => {
    setHaWidget('ha-custom-cards', {
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
          });

    const { container } = render(<HomeAssistantWidget id="ha-custom-cards" type="home-assistant" />);
    const grid = container.querySelector('.ha-card-grid') as HTMLElement;
    expect(grid.style.gridTemplateColumns).toMatch(/repeat\(3,\s*minmax\(0(px)?, 1fr\)\)/);
    expect(container.querySelector('.ha-card-wrapper')).not.toBeNull();
    expect(container.querySelector('.ha-card-unknown')?.textContent).toBe('Unsupported');
    setHaWidget('ha-renderer-edges', {
      view: {
        sections: [
          { title: 'Empty Section' },
          {
            cards: [
              { type: 'grid', cards: [{ type: 'tile', entity: 'switch.grid_def' }] },
              { type: 'entities', title: 'Empty Entities' },
              { type: 'entities', entities: ['', { entity: '' }] },
              { type: 'vertical-stack', cards: [{ type: 'tile', entity: 'switch.v_def' }] },
              { name: 'Just Name' }, { type: 'plain-type' }, {},
              { type: 'custom-foo', title: 'Foo Title' }, { type: 'custom-bar', name: 'Bar Name' },
            ],
          },
        ],
      },
      entities: {
        'switch.grid_def': { entity_id: 'switch.grid_def', state: 'off', attributes: {} },
        'switch.v_def': { entity_id: 'switch.v_def', state: 'off', attributes: {} },
      },
    });
    const { container: edgeCont } = render(<HomeAssistantWidget id="ha-renderer-edges" type="home-assistant" />);
    expect(edgeCont.querySelector('.ha-card-grid')).not.toBeNull();
    expect(edgeCont.querySelector('.ha-card-wrapper')).not.toBeNull();
    expect(edgeCont.querySelectorAll('.ha-divider').length).toBeGreaterThan(0);
    const unknownLabels = Array.from(edgeCont.querySelectorAll('.ha-card-unknown')).map((el) => el.textContent);
    expect(unknownLabels).toEqual(['Just Name', 'plain-type', 'Card', 'Foo Title', 'Bar Name']);
  });

  const buttonCardActionCases = [
    {
      name: 'scene default',
      card: { type: 'button', entity: 'scene.night_mode' },
      entity: { entity_id: 'scene.night_mode', state: 'scening', attributes: {} },
      wantBody: { domain: 'scene', service: 'turn_on', entity_id: 'scene.night_mode', service_data: { entity_id: 'scene.night_mode' } },
    },
    {
      name: 'script default',
      card: { type: 'button', entity: 'script.run_vac' },
      entity: { entity_id: 'script.run_vac', state: 'off', attributes: {} },
      wantBody: { domain: 'script', service: 'turn_on', entity_id: 'script.run_vac', service_data: { entity_id: 'script.run_vac' } },
    },
    {
      name: 'light default',
      card: { type: 'button', entity: 'light.lamp' },
      entity: { entity_id: 'light.lamp', state: 'off', attributes: {} },
      wantBody: { domain: 'light', service: 'toggle', entity_id: 'light.lamp', service_data: { entity_id: 'light.lamp' } },
    },
    {
      name: 'lock locked -> unlock',
      card: { type: 'button', entity: 'lock.door_locked' },
      entity: { entity_id: 'lock.door_locked', state: 'locked', attributes: {} },
      wantBody: { domain: 'lock', service: 'unlock', entity_id: 'lock.door_locked', service_data: { entity_id: 'lock.door_locked' } },
    },
    {
      name: 'lock unlocked -> lock',
      card: { type: 'button', entity: 'lock.door_unlocked' },
      entity: { entity_id: 'lock.door_unlocked', state: 'unlocked', attributes: {} },
      wantBody: { domain: 'lock', service: 'lock', entity_id: 'lock.door_unlocked', service_data: { entity_id: 'lock.door_unlocked' } },
    },
    {
      name: 'input_boolean default',
      card: { type: 'button', entity: 'input_boolean.guest_mode' },
      entity: { entity_id: 'input_boolean.guest_mode', state: 'off', attributes: {} },
      wantBody: { domain: 'input_boolean', service: 'toggle', entity_id: 'input_boolean.guest_mode', service_data: { entity_id: 'input_boolean.guest_mode' } },
    },
    {
      name: 'custom call_service with data',
      card: { type: 'button', entity: 'light.custom_call', tap_action: { action: 'call_service', service: 'toggle', data: { transition: 2 } } },
      entity: { entity_id: 'light.custom_call', state: 'on', attributes: {} },
      wantBody: { domain: 'light', service: 'toggle', entity_id: 'light.custom_call', service_data: { entity_id: 'light.custom_call', transition: 2 } },
    },
  ];

  it.each(buttonCardActionCases)('dispatches ButtonCard tap action: $name', async (tc) => {
    let callBody: unknown;
    vi.stubGlobal('fetch', vi.fn().mockImplementation((_url: string, options: RequestInit) => {
      callBody = JSON.parse(options.body as string);
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    }));

    setHaWidget('ha-btn-action-test', {
      cards: [tc.card],
      entities: { [tc.entity.entity_id]: tc.entity },
    });

    const { container } = render(<HomeAssistantWidget id="ha-btn-action-test" type="home-assistant" />);
    const btn = container.querySelector('.ha-card-button') as HTMLButtonElement;
    expect(btn).not.toBeNull();
    await act(async () => {
      fireEvent.click(btn);
    });
    expect(callBody).toEqual(tc.wantBody);
  });

  it('handles ButtonCard service prefixes, media services, and action failures', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);
    // Service prefixes, media services, toggle/more-info actions and entity-less buttons.
    setHaWidget('ha-button-variants', {
      cards: [
        {
          type: 'button', entity: 'light.opt_btn',
          tap_action: { action: 'call-service', service: '.toggle', service_data: { entity_id: 'light.opt_btn' } },
        },
        { type: 'button', entity: 'media_player.play_target', tap_action: { action: 'call-service', service: 'media_player.media_play' } },
        { type: 'button', entity: 'media_player.pause_target', tap_action: { action: 'call-service', service: 'media_player.media_pause' } },
        {
          type: 'button',
          entity: 'light.desk',
          tap_action: { action: 'call_service', service: 'toggle', service_data: { entity_id: 'light.desk' } },
        },
        { type: 'button', entity: 'light.desk', tap_action: { action: 'more-info' } },
        { type: 'button', entity: 'light.desk', tap_action: { action: 'toggle' } },
        { type: 'button', name: 'Restart Server', tap_action: { action: 'call-service', service: 'homeassistant.restart' } },
      ],
      entities: {
        'light.opt_btn': { entity_id: 'light.opt_btn', state: 'off', attributes: {} },
        'media_player.play_target': { entity_id: 'media_player.play_target', state: 'paused', attributes: {} },
        'media_player.pause_target': { entity_id: 'media_player.pause_target', state: 'playing', attributes: {} },
        'light.desk': { entity_id: 'light.desk', state: 'off', attributes: {} },
      },
    });
    const { container: variantCont } = render(<HomeAssistantWidget id="ha-button-variants" type="home-assistant" />);
    const variantButtons = variantCont.querySelectorAll('.ha-card-button');
    const callsBefore = fetchCalls.length;
    for (const b of Array.from(variantButtons)) {
      await act(async () => {
        fireEvent.click(b);
      });
    }
    expect(fetchCalls.slice(callsBefore).map((c) => JSON.parse(c.options.body as string))).toEqual([
      { domain: 'light', service: 'toggle', entity_id: 'light.opt_btn', service_data: { entity_id: 'light.opt_btn' } },
      {
        domain: 'media_player', service: 'media_play', entity_id: 'media_player.play_target', service_data: { entity_id: 'media_player.play_target' },
      },
      {
        domain: 'media_player', service: 'media_pause', entity_id: 'media_player.pause_target',
        service_data: { entity_id: 'media_player.pause_target' },
      },
      { domain: 'light', service: 'toggle', entity_id: 'light.desk', service_data: { entity_id: 'light.desk' } },
      { domain: 'light', service: 'toggle', entity_id: 'light.desk', service_data: { entity_id: 'light.desk' } },
      { domain: 'homeassistant', service: 'restart', service_data: {} },
    ]);
    expect(variantCont.querySelector('[data-entity-id="media_player.play_target"]')?.getAttribute('data-state')).toBe('playing');
    expect(variantCont.querySelector('[data-entity-id="media_player.pause_target"]')?.getAttribute('data-state')).toBe('paused');

    // An entity-less action failure still surfaces the alert banner.
    fetchMock.mockRejectedValueOnce(new Error('Server unavailable'));
    await act(async () => {
      fireEvent.click(itemAt(variantButtons, 6));
    });
    await waitFor(() => {
      expect(variantCont.querySelector('.ha-alert-banner')?.textContent).toContain('Server unavailable');
    });
  });

  it('drives ThermostatCard dial buttons and modes, defaulting missing attributes', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    setHaWidget('ha-tstat', {
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
          });

    const { container } = render(<HomeAssistantWidget id="ha-tstat" type="home-assistant" />);
    expect(container.querySelector('.ha-thermostat-title')?.textContent).toBe('Main Floor Thermostat');
    expect(container.querySelector('.ha-temp-val')?.textContent).toBe('69°');

    // Decrease temperature
    const decreaseBtn = container.querySelector('[aria-label="Decrease Temperature"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(decreaseBtn);
    });
    expect(JSON.parse(itemAt(fetchCalls, 0).options.body as string)).toEqual({
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
    expect(JSON.parse(itemAt(fetchCalls, 1).options.body as string)).toEqual({
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
    expect(JSON.parse(itemAt(fetchCalls, 2).options.body as string)).toEqual({
      domain: 'climate',
      service: 'set_hvac_mode',
      entity_id: 'climate.hallway',
      service_data: { entity_id: 'climate.hallway', hvac_mode: 'cool' },
    });
    // Missing and null (Go nil map) attributes fall back to a 70° target.
    setHaWidget('ha-tstat-def', {
      cards: [{ type: 'thermostat', entity: 'climate.bare' }, { type: 'thermostat', entity: 'climate.nullattrs' }],
      entities: {
        'climate.bare': { entity_id: 'climate.bare', state: 'off', attributes: {} },
        'climate.nullattrs': { entity_id: 'climate.nullattrs', state: 'off', attributes: null },
      },
    });
    const { container: defCont } = render(<HomeAssistantWidget id="ha-tstat-def" type="home-assistant" />);
    expect(Array.from(defCont.querySelectorAll('.ha-target-num')).map((el) => el.textContent)).toEqual(['70°', '70°']);
    expect(defCont.querySelector('[data-entity-id="climate.nullattrs"] .ha-thermostat-title')?.textContent).toBe('nullattrs');
    const barePlus = defCont.querySelector('[data-entity-id="climate.bare"] [aria-label="Increase Temperature"]') as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(barePlus);
    });
    expect(JSON.parse(itemAt(fetchCalls, 3).options.body as string).service_data).toEqual({ entity_id: 'climate.bare', temperature: 71 });
  });

  it('refreshes PictureEntityCard cameras, toggles other entities and falls back without an image', async () => {
    const fetchCalls: { url: string; options: RequestInit }[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options: RequestInit) => {
      fetchCalls.push({ url, options });
      return Promise.resolve(new Response(JSON.stringify({ status: 'ok' })));
    });
    vi.stubGlobal('fetch', fetchMock);

    setHaWidget('ha-pic', {
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
          });

    const { container } = render(<HomeAssistantWidget id="ha-pic" type="home-assistant" />);
    const picCards = container.querySelectorAll('.ha-card-picture');

    // 1. Camera tap triggers refreshCamera and updates query timestamp
    await act(async () => {
      fireEvent.click(itemAt(picCards, 0));
    });
    const yardImg = itemAt(picCards, 0).querySelector('img') as HTMLImageElement;
    expect(yardImg.src).toContain('?ts=');

    // 2. Non-camera picture card tap triggers toggle
    await act(async () => {
      fireEvent.click(itemAt(picCards, 1));
    });
    expect(JSON.parse(itemAt(fetchCalls, 0).options.body as string)).toEqual({
      domain: 'switch',
      service: 'toggle',
      entity_id: 'switch.garden_fountain',
      service_data: { entity_id: 'switch.garden_fountain' },
    });

    // 3. Camera with no image renders fallback container
    expect(itemAt(picCards, 2).querySelector('.ha-picture-fallback')).not.toBeNull();
    setHaWidget('ha-pic-branches', {
      cards: [
        { type: 'picture-entity', entity: 'switch.cam_power', camera_image: 'camera.yard' },
        { type: 'picture-entity', entity: 'light.yard_light' },
        { type: 'picture-entity', entity: 'sensor.no_pic' },
        { type: 'picture-entity', entity: 'camera.nullattrs', camera_image: 'camera.nullattrs' },
      ],
      entities: {
        'switch.cam_power': { entity_id: 'switch.cam_power', state: 'on', attributes: {} },
        'camera.yard': { entity_id: 'camera.yard', state: 'idle', attributes: { entity_picture: '/yard.jpg' } },
        'light.yard_light': { entity_id: 'light.yard_light', state: 'off', attributes: {} },
        'sensor.no_pic': { entity_id: 'sensor.no_pic', state: '10', attributes: {} },
        'camera.nullattrs': { entity_id: 'camera.nullattrs', state: 'idle', attributes: null },
      },
    });
    const { container: branchCont } = render(<HomeAssistantWidget id="ha-pic-branches" type="home-assistant" />);
    const branchCards = branchCont.querySelectorAll('.ha-card-picture');
    expect(branchCards.length).toBe(4);
    await act(async () => {
      fireEvent.click(itemAt(branchCards, 0));
      fireEvent.click(itemAt(branchCards, 1));
    });
    expect(fetchCalls.slice(1).map((c) => JSON.parse(c.options.body as string).entity_id)).toEqual(['light.yard_light']);
    expect((itemAt(branchCards, 0).querySelector('img') as HTMLImageElement).src).toContain('/yard.jpg?ts=');
    expect(itemAt(branchCards, 2).querySelector('.ha-picture-fallback')).not.toBeNull();
  });

  it('guards non-entity items in CardRenderer and handles getEntityName with empty id', () => {
    expect(getEntityName(undefined, '')).toBe('');

    setHaWidget('ha-dividers', {
            title: 'Dividers and Edge Items',
            cards: [
              { type: 'divider' },
              {
                type: 'entities',
                title: 'Entities with Dividers',
                entities: [
                  { type: 'divider' },
                  { name: 'Row with no entity' },
                  'light.kitchen',
                ],
              },
            ],
            entities: {
              'light.kitchen': { entity_id: 'light.kitchen', state: 'on', attributes: { friendly_name: 'Kitchen Light' } },
            },
          });

    const { container } = render(<HomeAssistantWidget id="ha-dividers" type="home-assistant" />);
    const dividers = container.querySelectorAll('.ha-divider');
    expect(dividers.length).toBeGreaterThanOrEqual(2);
    expect(container.querySelector('[data-entity-id="light.kitchen"]')).not.toBeNull();
  });

  it('handles PictureEntityCard camera_image entity resolution and ts join with existing query params', async () => {
    setHaWidget('ha-pic-edge', {
            cards: [
              {
                type: 'picture-entity',
                entity: 'camera.front_gate',
                camera_image: 'camera.front_gate',
              },
            ],
            entities: {
              'camera.front_gate': {
                entity_id: 'camera.front_gate',
                state: 'idle',
                attributes: {
                  entity_picture: '/api/camera_proxy/camera.front_gate?token=abc123xyz',
                  friendly_name: 'Front Gate Camera',
                },
              },
            },
          });

    const { container } = render(<HomeAssistantWidget id="ha-pic-edge" type="home-assistant" />);
    const picCard = container.querySelector('.ha-card-picture') as HTMLElement;
    expect(picCard).not.toBeNull();

    const imgBefore = picCard.querySelector('img') as HTMLImageElement;
    expect(imgBefore.src).toContain('/api/camera_proxy/camera.front_gate?token=abc123xyz');
    expect(imgBefore.src).not.toContain('&ts=');

    // Tap to refresh camera and verify &ts= is used instead of ?ts=
    await act(async () => {
      fireEvent.click(picCard);
    });

    const imgAfter = picCard.querySelector('img') as HTMLImageElement;
    expect(imgAfter.src).toContain('?token=abc123xyz&ts=');
  });

  it('drops optimistic overrides when a new snapshot arrives', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 200 })));
    const snapshot = (state: string) => ({
      cards: [{ type: 'tile', entity: 'switch.living' }],
      entities: { 'switch.living': { entity_id: 'switch.living', state, attributes: {} } },
    });
    setHaWidget('ha-snapshot-reset', snapshot('off'));
    const { container, rerender } = render(<HomeAssistantWidget id="ha-snapshot-reset" type="home-assistant" />);
    await act(async () => {
      fireEvent.click(container.querySelector('[data-action="toggle"]') as HTMLButtonElement);
    });
    expect(container.querySelector('[data-entity-id="switch.living"]')?.getAttribute('data-state')).toBe('on');
    setHaWidget('ha-snapshot-reset', snapshot('off'));
    rerender(<HomeAssistantWidget id="ha-snapshot-reset" type="home-assistant" />);
    expect(container.querySelector('[data-entity-id="switch.living"]')?.getAttribute('data-state')).toBe('off');
  });

  const applyOptimisticUpdateTests = [
    { name: 'light turn_on', state: 'off', domain: 'light', service: 'turn_on', data: undefined, wantState: 'on', wantAttrs: {} },
    {
      name: 'light turn_on with brightness',
      state: 'off', domain: 'light', service: 'turn_on', data: { brightness: 128 }, wantState: 'on', wantAttrs: { brightness: 128 },
    },
    {
      name: 'fan turn_on with percentage',
      state: 'off', domain: 'fan', service: 'turn_on', data: { percentage: 60 }, wantState: 'on', wantAttrs: { percentage: 60 },
    },
    { name: 'light turn_off', state: 'on', domain: 'light', service: 'turn_off', data: undefined, wantState: 'off', wantAttrs: {} },
    { name: 'switch toggle off to on', state: 'off', domain: 'switch', service: 'toggle', data: undefined, wantState: 'on', wantAttrs: {} },
    { name: 'switch toggle on to off', state: 'on', domain: 'switch', service: 'toggle', data: undefined, wantState: 'off', wantAttrs: {} },
    {
      name: 'fan set_percentage above zero turns on',
      state: 'off', domain: 'fan', service: 'set_percentage', data: { percentage: 66 }, wantState: 'on', wantAttrs: { percentage: 66 },
    },
    {
      name: 'fan set_percentage zero turns off',
      state: 'on', domain: 'fan', service: 'set_percentage', data: { percentage: 0 }, wantState: 'off', wantAttrs: { percentage: 0 },
    },
    { name: 'lock lock', state: 'off', domain: 'lock', service: 'lock', data: undefined, wantState: 'locked', wantAttrs: {} },
    { name: 'lock unlock', state: 'off', domain: 'lock', service: 'unlock', data: undefined, wantState: 'unlocked', wantAttrs: {} },
    { name: 'cover open_cover', state: 'off', domain: 'cover', service: 'open_cover', data: undefined, wantState: 'open', wantAttrs: {} },
    { name: 'cover close_cover', state: 'open', domain: 'cover', service: 'close_cover', data: undefined, wantState: 'closed', wantAttrs: {} },
    {
      name: 'cover stop_cover keeps state', state: 'open', domain: 'cover', service: 'stop_cover', data: undefined, wantState: 'open', wantAttrs: {},
    },
    {
      name: 'climate set_temperature',
      state: 'off', domain: 'climate', service: 'set_temperature', data: { temperature: 72 }, wantState: 'off', wantAttrs: { temperature: 72 },
    },
    {
      name: 'climate set_hvac_mode',
      state: 'off', domain: 'climate', service: 'set_hvac_mode', data: { hvac_mode: 'heat' }, wantState: 'heat', wantAttrs: {},
    },
    { name: 'media_play', state: 'off', domain: 'media_player', service: 'media_play', data: undefined, wantState: 'playing', wantAttrs: {} },
    { name: 'media_pause', state: 'off', domain: 'media_player', service: 'media_pause', data: undefined, wantState: 'paused', wantAttrs: {} },
    {
      name: 'media_play_pause while playing',
      state: 'playing', domain: 'media_player', service: 'media_play_pause', data: undefined, wantState: 'paused', wantAttrs: {},
    },
    {
      name: 'media_play_pause while paused',
      state: 'paused', domain: 'media_player', service: 'media_play_pause', data: undefined, wantState: 'playing', wantAttrs: {},
    },
    {
      name: 'media volume_up keeps state',
      state: 'playing', domain: 'media_player', service: 'volume_up', data: undefined, wantState: 'playing', wantAttrs: {},
    },
    { name: 'unknown domain is unchanged', state: 'off', domain: 'sensor', service: 'update', data: undefined, wantState: 'off', wantAttrs: {} },
  ];

  it.each(applyOptimisticUpdateTests)('applyOptimisticUpdate: $name', (tc) => {
    const curr = { entity_id: `${tc.domain}.alpha`, state: tc.state, attributes: {} };
    expect(applyOptimisticUpdate(curr, tc.domain, tc.service, tc.data)).toEqual({ ...curr, state: tc.wantState, attributes: tc.wantAttrs });
  });

  const formatStateLabelTests = [
    { name: 'light on with brightness', domain: 'light', state: 'on', attrs: { brightness: 255 }, want: 'On · 100%' },
    { name: 'light on without attrs', domain: 'light', state: 'on', attrs: undefined, want: 'On' },
    { name: 'light on without brightness', domain: 'light', state: 'on', attrs: {}, want: 'On' },
    { name: 'light off', domain: 'light', state: 'off', attrs: undefined, want: 'Off' },
    { name: 'climate with current temperature', domain: 'climate', state: 'heat', attrs: { current_temperature: 71 }, want: 'heat · 71°' },
    { name: 'climate without attrs', domain: 'climate', state: 'heat', attrs: undefined, want: 'heat' },
    { name: 'climate without current temperature', domain: 'climate', state: 'auto', attrs: {}, want: 'auto' },
    {
      name: 'media title and artist',
      domain: 'media_player', state: 'playing', attrs: { media_title: 'Song', media_artist: 'Band' }, want: 'Song - Band',
    },
    { name: 'media title only', domain: 'media_player', state: 'playing', attrs: { media_title: 'Solo' }, want: 'Solo' },
    { name: 'media without attrs', domain: 'media_player', state: 'idle', attrs: undefined, want: 'idle' },
    { name: 'media without title', domain: 'media_player', state: 'idle', attrs: {}, want: 'idle' },
    { name: 'sensor with unit', domain: 'sensor', state: '42', attrs: { unit_of_measurement: '%' }, want: '42 %' },
    { name: 'sensor without attrs', domain: 'sensor', state: '42', attrs: undefined, want: '42' },
    { name: 'sensor without unit', domain: 'sensor', state: 'online', attrs: {}, want: 'online' },
    { name: 'other domain returns state', domain: 'lock', state: 'locked', attrs: undefined, want: 'locked' },
  ];

  it.each(formatStateLabelTests)('formatStateLabel: $name', (tc) => {
    expect(formatStateLabel(tc.domain, tc.state, tc.attrs)).toBe(tc.want);
  });

  const getBinaryLabelTests = [
    { isOn: true, dc: 'door', want: 'Open' },
    { isOn: false, dc: 'door', want: 'Closed' },
    { isOn: true, dc: 'window', want: 'Open' },
    { isOn: false, dc: 'window', want: 'Closed' },
    { isOn: true, dc: 'occupancy', want: 'Occupied' },
    { isOn: false, dc: 'occupancy', want: 'Clear' },
    { isOn: true, dc: 'presence', want: 'Home' },
    { isOn: false, dc: 'presence', want: 'Away' },
    { isOn: true, dc: 'motion', want: 'Motion' },
    { isOn: false, dc: 'motion', want: 'Clear' },
    { isOn: true, dc: 'unknown', want: 'On' },
    { isOn: false, dc: 'unknown', want: 'Off' },
    { isOn: true, dc: undefined, want: 'On' },
    { isOn: false, dc: undefined, want: 'Off' },
  ];

  it.each(getBinaryLabelTests)('getBinaryLabel($isOn, $dc) is $want', (tc) => {
    expect(getBinaryLabel(tc.isOn, tc.dc)).toBe(tc.want);
  });

  it('covers inFlight state rendering and disabled interactions', async () => {
    let resolveAction!: (v: unknown) => void;
    const fetchMock = vi.fn().mockImplementation(() => new Promise((resolve) => { resolveAction = resolve; }));
    vi.stubGlobal('fetch', fetchMock);

    setHaWidget('ha-inflight-all', {
      cards: [
        { type: 'button', entity: 'switch.btn_flight' },
        { type: 'button', name: 'Target Action Button', tap_action: { action: 'call-service', service: 'scene.turn_on', target: { entity_id: 'scene.target_flight' } } },
        { type: 'button', name: 'Data Action Button', tap_action: { action: 'call_service', service: 'script.turn_on', data: { entity_id: 'script.data_flight' } } },
        { type: 'tile', entity: 'light.tile_flight' },
        { type: 'tile', entity: 'fan.fan_flight' },
        { type: 'tile', entity: 'lock.lock_flight' },
        { type: 'tile', entity: 'cover.cov_flight' },
        { type: 'thermostat', entity: 'climate.clim_flight' },
      ],
      entities: {
        'switch.btn_flight': { entity_id: 'switch.btn_flight', state: 'off', attributes: {} },
        'light.tile_flight': { entity_id: 'light.tile_flight', state: 'on', attributes: { brightness: 120 } },
        'fan.fan_flight': { entity_id: 'fan.fan_flight', state: 'on', attributes: { percentage: 50 } },
        'lock.lock_flight': { entity_id: 'lock.lock_flight', state: 'locked', attributes: {} },
        'cover.cov_flight': { entity_id: 'cover.cov_flight', state: 'open', attributes: {} },
        'climate.clim_flight': { entity_id: 'climate.clim_flight', state: 'heat', attributes: { temperature: 70 } },
      },
    });

    const { container } = render(<HomeAssistantWidget id="ha-inflight-all" type="home-assistant" />);
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('.ha-card-button'));
    expect(buttons).toHaveLength(3);

    for (const b of buttons) {
      await act(async () => { fireEvent.click(b); });
      expect(b.disabled).toBe(true);
      await act(async () => { fireEvent.click(b); });
      await act(async () => { resolveAction(new Response(JSON.stringify({ status: 'ok' }))); });
      expect(b.disabled).toBe(false);
    }
  });

  it('renders unavailable fallbacks for missing entities across all card types', () => {
    setHaWidget('ha-missing-all', {
      cards: [
        { type: 'entities', entities: ['light.missing_row'] },
        { type: 'tile', entity: 'light.tile_name', name: 'Name' }, { type: 'tile', entity: 'light.tile_title', title: 'Title' }, { type: 'tile', entity: 'light.tile_none' },
        { type: 'button', entity: 'light.btn_name', name: 'Btn', icon: '⭐' }, { type: 'button', entity: 'light.btn_title', title: 'Btn' }, { type: 'button', entity: 'light.btn_none' },
        { type: 'thermostat', entity: 'climate.missing_clim' }, { type: 'picture-entity', entity: 'camera.missing_cam' },
      ],
      entities: {},
    });

    const { container } = render(<HomeAssistantWidget id="ha-missing-all" type="home-assistant" />);
    expect(container.querySelector('.ha-entity-row')?.getAttribute('data-state')).toBe('unavailable');
    expect(container.querySelectorAll('.ha-card-tile').length).toBe(3);
    expect(container.querySelectorAll('.ha-card-button').length).toBe(3);
    expect(container.querySelector('.ha-card-thermostat')).not.toBeNull();
    expect(container.querySelector('.ha-card-picture')).not.toBeNull();
  });
});
