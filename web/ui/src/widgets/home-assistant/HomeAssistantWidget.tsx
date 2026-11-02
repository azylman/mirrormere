import { memo, useCallback, useMemo, useState } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';

export interface EntityState {
  entity_id: string; state: string; attributes: Record<string, unknown>;
  last_changed?: string; last_updated?: string;
}

interface LovelaceCard {
  type: string; title?: string; entity?: string;
  entities?: (string | { entity?: string; type?: string; name?: string; icon?: string; [k: string]: unknown })[];
  cards?: LovelaceCard[]; name?: string; icon?: string; image?: string; camera_image?: string;
  tap_action?: Record<string, unknown>; columns?: number; [k: string]: unknown;
}

interface HomeAssistantSnapshot {
  title?: string; view?: { title?: string; sections?: { title?: string; cards?: LovelaceCard[] }[]; cards?: LovelaceCard[] };
  cards?: LovelaceCard[]; entities?: Record<string, EntityState>; last_updated?: string;
}

export interface ActionPayload {
  domain: string; service: string; entity_id?: string; service_data?: Record<string, unknown>;
}

export function getDomain(entityId: string): string {
  if (!entityId) return '';
  return entityId.split('.')[0].toLowerCase();
}

export function getEntityName(entity: EntityState | undefined, entityId: string, customName?: string): string {
  if (!entityId) return '';
  if (customName && customName.trim() !== '') return customName;
  if (entity?.attributes?.friendly_name && typeof entity.attributes.friendly_name === 'string') {
    return entity.attributes.friendly_name;
  }
  const parts = entityId.split('.');
  return parts.length > 1 ? parts[1].replace(/_/g, ' ') : entityId;
}

const DOMAIN_ICONS: Record<string, string | ((s?: string) => string)> = {
  light: (s) => (s === 'on' ? '💡' : '○'), switch: (s) => (s === 'on' ? '🔌' : '○'),
  fan: (s) => (s === 'on' ? '🌀' : '🪭'), cover: (s) => (s === 'open' ? '🪟' : '🚪'),
  lock: (s) => (s === 'locked' ? '🔒' : '🔓'), climate: '🌡️',
  media_player: (s) => (s === 'playing' ? '🔊' : '🎵'), camera: '📹',
  scene: '🎬', script: '⚡', sensor: '📊', binary_sensor: (s) => (s === 'on' ? '🔔' : '🔕'),
};

export function getDomainIcon(domain: string, state?: string): string {
  const icon = DOMAIN_ICONS[domain];
  return typeof icon === 'function' ? icon(state) : (icon ?? '⚙️');
}

export async function dispatchWidgetAction(widgetId: string, payload: ActionPayload, fetchImpl = fetch): Promise<unknown> {
  const url = `/api/widgets/${encodeURIComponent(widgetId)}/action`;
  const body: Record<string, unknown> = { domain: payload.domain, service: payload.service };
  if (payload.entity_id) body.entity_id = payload.entity_id;
  if (payload.service_data) {
    body.service_data = { ...(payload.entity_id ? { entity_id: payload.entity_id } : {}), ...payload.service_data };
  } else if (payload.entity_id) {
    body.service_data = { entity_id: payload.entity_id };
  }
  const res = await fetchImpl(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`Widget action failed (HTTP ${res.status}): ${await res.text().catch(() => '')}`);
  return await res.json().catch(() => ({}));
}

export function applyOptimisticUpdate(curr: EntityState, domain: string, service: string, data?: Record<string, unknown>): EntityState {
  let state = curr.state;
  const attrs = { ...curr.attributes };
  if (domain === 'light' || domain === 'switch' || domain === 'fan') {
    if (service === 'toggle') state = curr.state === 'on' ? 'off' : 'on';
    else if (service === 'turn_on') {
      state = 'on';
      if (data?.brightness !== undefined) attrs.brightness = data.brightness;
      if (data?.percentage !== undefined) attrs.percentage = data.percentage;
    } else if (service === 'turn_off') state = 'off';
  } else if (domain === 'lock') state = service === 'lock' ? 'locked' : 'unlocked';
  else if (domain === 'cover') state = service === 'open_cover' ? 'open' : 'closed';
  else if (domain === 'climate') {
    if (data?.temperature !== undefined) attrs.temperature = data.temperature;
    if (data?.hvac_mode !== undefined) state = String(data.hvac_mode);
  } else if (domain === 'media_player') {
    state = service === 'media_play' ? 'playing' : service === 'media_pause' ? 'paused' : curr.state === 'playing' ? 'paused' : 'playing';
  }
  return { ...curr, state, attributes: attrs };
}

export const HomeAssistantWidget = memo(function HomeAssistantWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const data = entry?.data as HomeAssistantSnapshot | undefined;
  const isError = entry?.state === 'error';
  const [overrides, setOverrides] = useState<Record<string, EntityState>>({});
  const [prevData, setPrevData] = useState(data);
  const [inFlight, setInFlight] = useState<Record<string, boolean>>({});
  const [actionError, setActionError] = useState<string | null>(null);
  const [cameraTimestamps, setCameraTimestamps] = useState<Record<string, number>>({});

  if (data !== prevData) {
    setPrevData(data);
    setOverrides({});
  }

  const entities = useMemo(() => ({ ...(data?.entities || {}), ...overrides }), [data?.entities, overrides]);

  const sendAction = useCallback(async (payload: ActionPayload) => {
    const entId = payload.entity_id;
    if (entId) setInFlight((prev) => ({ ...prev, [entId]: true }));
    setActionError(null);
    if (entId && entities[entId]) {
      setOverrides((prev) => ({ ...prev, [entId]: applyOptimisticUpdate(entities[entId], payload.domain, payload.service, payload.service_data) }));
    }
    try {
      await dispatchWidgetAction(id, payload);
    } catch (err) {
      if (entId) {
        setOverrides((prev) => {
          const next = { ...prev };
          delete next[entId];
          return next;
        });
      }
      setActionError(err instanceof Error ? err.message : 'Action failed');
    } finally {
      if (entId) {
        setInFlight((prev) => {
          const next = { ...prev };
          delete next[entId];
          return next;
        });
      }
    }
  }, [id, entities]);

  const refreshCamera = useCallback((entityId: string) => {
    setCameraTimestamps((prev) => ({ ...prev, [entityId]: Date.now() }));
  }, []);

  const title = data?.title || data?.view?.title || 'Home Assistant';
  const cardsToRender = useMemo(() => {
    if (data?.view?.sections?.length) return null;
    if (data?.cards?.length) return data.cards;
    if (data?.view?.cards?.length) return data.view.cards;
    if (entities && Object.keys(entities).length > 0) {
      return Object.keys(entities).map((entId) => {
        const d = getDomain(entId);
        const t = d === 'climate' ? 'thermostat' : d === 'camera' ? 'picture-entity' : d === 'scene' || d === 'script' ? 'button' : 'tile';
        return { type: t, entity: entId };
      });
    }
    return [];
  }, [data?.view?.sections, data?.cards, data?.view?.cards, entities]);

  if (isError) {
    return (
      <div className="widget-home-assistant widget-ha-error" data-widget-type="home-assistant">
        <div className="ha-error-message">Unable to load Home Assistant dashboard.</div>
      </div>
    );
  }

  if (!data || (!data.cards && !data.view && Object.keys(entities).length === 0)) {
    return (
      <div className="widget-home-assistant widget-ha-loading" data-widget-type="home-assistant">
        <div className="ha-loading-spinner">Loading Home Assistant...</div>
      </div>
    );
  }

  const ctx: Ctx = { entities, inFlight, sendAction, refreshCamera, cameraTimestamps };
  const hasSections = Boolean(data.view?.sections && data.view.sections.length > 0);

  return (
    <div className="widget-home-assistant" data-widget-type="home-assistant" data-widget-id={id}>
      <header className="ha-header">
        <div className="ha-title-wrap"><span className="ha-icon">🏠</span><h2 className="ha-title">{title}</h2></div>
        {data.last_updated && (
          <span className="ha-last-updated" title={data.last_updated}>
            {new Date(data.last_updated).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </span>
        )}
      </header>

      {actionError && (
        <div className="ha-alert-banner" role="alert">
          <span>⚠️ {actionError}</span>
          <button type="button" className="ha-banner-dismiss" onClick={() => setActionError(null)}>✕</button>
        </div>
      )}

      <main className="ha-content">
        {hasSections ? (
          <div className="ha-sections-layout">
            {data.view?.sections?.map((section, sIdx) => (
              <section key={`sec-${sIdx}`} className="ha-section" data-section-index={sIdx}>
                {section.title && <h3 className="ha-section-title">{section.title}</h3>}
                <div className="ha-cards-grid">
                  {(section.cards || []).map((card, cIdx) => <CardRenderer key={`card-${sIdx}-${cIdx}`} card={card} ctx={ctx} />)}
                </div>
              </section>
            ))}
          </div>
        ) : (
          <div className="ha-cards-grid">
            {(cardsToRender || []).map((card, cIdx) => <CardRenderer key={`card-${cIdx}`} card={card} ctx={ctx} />)}
          </div>
        )}
      </main>
    </div>
  );
});

interface Ctx {
  entities: Record<string, EntityState>; inFlight: Record<string, boolean>;
  sendAction: (payload: ActionPayload) => Promise<void>; refreshCamera: (entityId: string) => void;
  cameraTimestamps: Record<string, number>;
}

function CardRenderer({ card, ctx }: { card: LovelaceCard; ctx: Ctx }) {
  const cardType = (card.type || '').toLowerCase();
  if (cardType === 'divider') return <div className="ha-card ha-card-divider ha-divider" />;

  if (cardType === 'grid' && card.cards) {
    return (
      <div className="ha-card ha-card-grid" style={{ gridTemplateColumns: `repeat(${card.columns || 2}, minmax(0, 1fr))` }}>
        {card.cards.map((sub, idx) => <CardRenderer key={`sub-${idx}`} card={sub} ctx={ctx} />)}
      </div>
    );
  }

  if (cardType === 'entities') {
    return (
      <div className="ha-card ha-card-entities">
        {card.title && <h4 className="ha-card-title">{card.title}</h4>}
        <div className="ha-entity-list">
          {(card.entities || []).map((item, idx) => {
            if (typeof item === 'object' && item !== null && (item.type === 'divider' || !item.entity)) return <div key={`div-${idx}`} className="ha-divider" />;
            const entId = typeof item === 'string' ? item : item.entity;
            if (!entId) return <div key={`div-${idx}`} className="ha-divider" />;
            const customName = typeof item === 'object' && item !== null ? item.name : undefined;
            return <EntityControlRow key={`ent-${idx}-${entId}`} entityId={entId} entity={ctx.entities[entId]} customName={customName} inFlight={Boolean(ctx.inFlight[entId])} sendAction={ctx.sendAction} refreshCamera={ctx.refreshCamera} />;
          })}
        </div>
      </div>
    );
  }

  const entId = card.entity || '';
  if (cardType === 'thermostat') return <ThermostatCard entityId={entId} entity={ctx.entities[entId]} title={card.title} inFlight={Boolean(ctx.inFlight[entId])} sendAction={ctx.sendAction} />;
  if (cardType === 'picture-entity') {
    const ts = ctx.cameraTimestamps[entId] || (card.camera_image ? ctx.cameraTimestamps[card.camera_image] : undefined);
    return <PictureEntityCard card={card} entityId={entId} entity={ctx.entities[entId]} entities={ctx.entities} refreshCamera={ctx.refreshCamera} cameraTimestamp={ts} sendAction={ctx.sendAction} />;
  }
  if (cardType === 'button') return <ButtonCard card={card} entityId={entId} entity={ctx.entities[entId]} inFlight={Boolean(ctx.inFlight[entId])} sendAction={ctx.sendAction} />;
  if (entId) return <TileCard entityId={entId} entity={ctx.entities[entId]} card={card} inFlight={Boolean(ctx.inFlight[entId])} sendAction={ctx.sendAction} refreshCamera={ctx.refreshCamera} />;

  if (card.cards?.length) {
    return (
      <div className="ha-card ha-card-wrapper">
        {card.title && <h4 className="ha-card-title">{card.title}</h4>}
        {card.cards.map((c, i) => <CardRenderer key={`nested-${i}`} card={c} ctx={ctx} />)}
      </div>
    );
  }
  return <div className="ha-card ha-card-unknown">{card.title || card.name || cardType || 'Card'}</div>;
}

function EntityControlRow({ entityId, entity, customName, inFlight, sendAction, refreshCamera }: {
  entityId: string; entity?: EntityState; customName?: string; inFlight: boolean; sendAction: (p: ActionPayload) => Promise<void>; refreshCamera: (id: string) => void;
}) {
  const domain = getDomain(entityId), name = getEntityName(entity, entityId, customName), state = entity?.state ?? 'unavailable';
  return (
    <div className={`ha-entity-row ha-domain-${domain} ${state === 'on' ? 'is-active' : ''}`} data-entity-id={entityId} data-domain={domain} data-state={state}>
      <div className="ha-row-left"><span className="ha-row-icon">{getDomainIcon(domain, state)}</span><div className="ha-row-meta"><div className="ha-row-name">{name}</div><div className="ha-row-state-label">{formatStateLabel(domain, state, entity?.attributes)}</div></div></div>
      <div className="ha-row-actions" data-prevent-screen-swipe=""><DomainTouchControls domain={domain} entityId={entityId} entity={entity} inFlight={inFlight} sendAction={sendAction} refreshCamera={refreshCamera} /></div>
    </div>
  );
}

function TileCard({ entityId, entity, card, inFlight, sendAction, refreshCamera }: {
  entityId: string; entity?: EntityState; card: LovelaceCard; inFlight: boolean; sendAction: (p: ActionPayload) => Promise<void>; refreshCamera: (id: string) => void;
}) {
  const domain = getDomain(entityId), name = getEntityName(entity, entityId, card.name || card.title), state = entity?.state ?? 'unavailable';
  return (
    <div className={`ha-card ha-card-tile ha-domain-${domain} ${state === 'on' ? 'is-active' : ''}`} data-entity-id={entityId} data-domain={domain} data-state={state}>
      <div className="ha-tile-header"><span className="ha-tile-icon">{card.icon || getDomainIcon(domain, state)}</span><div className="ha-tile-meta"><div className="ha-tile-name">{name}</div><div className="ha-tile-state">{formatStateLabel(domain, state, entity?.attributes)}</div></div></div>
      <div className="ha-tile-controls" data-prevent-screen-swipe=""><DomainTouchControls domain={domain} entityId={entityId} entity={entity} inFlight={inFlight} sendAction={sendAction} refreshCamera={refreshCamera} /></div>
    </div>
  );
}

function ButtonCard({ card, entityId, entity, inFlight, sendAction }: {
  card: LovelaceCard; entityId: string; entity?: EntityState; inFlight: boolean; sendAction: (p: ActionPayload) => Promise<void>;
}) {
  const domain = getDomain(entityId), name = getEntityName(entity, entityId, card.name || card.title);
  const state = entity?.state ?? 'idle', icon = card.icon || getDomainIcon(domain, state);
  const handleTap = () => {
    if (inFlight) return;
    if (card.tap_action) {
      const act = card.tap_action;
      if (act.action === 'call-service' || act.action === 'call_service') {
        const full = String(act.service || ''), [dom, srv] = full.includes('.') ? full.split('.') : [domain, full];
        const tgt = act.target as Record<string, unknown> | undefined, svc = act.service_data as Record<string, unknown> | undefined;
        const ent = typeof tgt?.entity_id === 'string' ? tgt.entity_id : typeof svc?.entity_id === 'string' ? svc.entity_id : entityId;
        void sendAction({ domain: dom || domain, service: srv, entity_id: ent, service_data: (act.service_data || act.data || {}) as Record<string, unknown> });
        return;
      }
    }
    if (domain === 'scene' || domain === 'script') void sendAction({ domain, service: 'turn_on', entity_id: entityId });
    else if (domain === 'lock') void sendAction({ domain: 'lock', service: state === 'locked' ? 'unlock' : 'lock', entity_id: entityId });
    else void sendAction({ domain, service: 'toggle', entity_id: entityId });
  };
  return (
    <button type="button" className={`ha-card ha-card-button ha-btn-touch ha-domain-${domain} ${state === 'on' ? 'is-active' : ''}`} data-entity-id={entityId} data-domain={domain} data-state={state} data-prevent-screen-swipe="" disabled={inFlight} onClick={handleTap}>
      <span className="ha-btn-card-icon">{icon}</span><span className="ha-btn-card-name">{name}</span><span className="ha-btn-card-state">{state}</span>
    </button>
  );
}

function ThermostatCard({ entityId, entity, title, inFlight, sendAction }: {
  entityId: string; entity?: EntityState; title?: string; inFlight: boolean; sendAction: (p: ActionPayload) => Promise<void>;
}) {
  const name = title || getEntityName(entity, entityId);
  const state = entity?.state ?? 'off';
  const cur = entity?.attributes?.current_temperature as number | undefined;
  const target = (entity?.attributes?.temperature as number | undefined) ?? 70;
  const modes = (entity?.attributes?.hvac_modes as string[] | undefined) || ['off', 'heat', 'cool', 'auto'];
  const act = (srv: string, data: Record<string, unknown>) => sendAction({ domain: 'climate', service: srv, entity_id: entityId, service_data: data });

  return (
    <div className={`ha-card ha-card-thermostat is-${state}`} data-entity-id={entityId} data-domain="climate" data-state={state}>
      <div className="ha-thermostat-header"><span className="ha-thermostat-icon">🌡️</span><div className="ha-thermostat-title">{name}</div><div className="ha-thermostat-state-badge">{state.toUpperCase()}</div></div>
      <div className="ha-thermostat-body">
        {cur !== undefined && <div className="ha-current-temp" title="Current Temperature">Current: <span className="ha-temp-val">{cur}°</span></div>}
        <div className="ha-target-temp-dial" data-prevent-screen-swipe="">
          <button type="button" className="ha-btn-circle" disabled={inFlight} onClick={() => void act('set_temperature', { temperature: target - 1 })} aria-label="Decrease Temperature">-</button>
          <div className="ha-target-display"><span className="ha-target-num">{target}°</span><span className="ha-target-lbl">Target</span></div>
          <button type="button" className="ha-btn-circle" disabled={inFlight} onClick={() => void act('set_temperature', { temperature: target + 1 })} aria-label="Increase Temperature">+</button>
        </div>
      </div>
      <div className="ha-thermostat-modes" data-prevent-screen-swipe="">
        {modes.map((m) => <button key={m} type="button" className={`ha-btn-mode ${state === m ? 'active' : ''}`} disabled={inFlight} onClick={() => void act('set_hvac_mode', { hvac_mode: m })}>{m}</button>)}
      </div>
    </div>
  );
}

function PictureEntityCard({ card, entityId, entity, entities, refreshCamera, cameraTimestamp, sendAction }: {
  card: LovelaceCard; entityId: string; entity?: EntityState; entities: Record<string, EntityState>;
  refreshCamera: (id: string) => void; cameraTimestamp?: number; sendAction: (p: ActionPayload) => Promise<void>;
}) {
  const name = getEntityName(entity, entityId, (card.title || card.name) as string | undefined);
  const state = entity?.state ?? 'idle';
  const camPic = card.camera_image ? (entities[card.camera_image]?.attributes?.entity_picture as string | undefined) : undefined;
  const pic = (entity?.attributes?.entity_picture as string | undefined) || camPic || (card.image as string | undefined) || (card.camera_image as string | undefined);
  const imageSrc = pic ? (cameraTimestamp ? `${pic}${pic.includes('?') ? '&' : '?'}ts=${cameraTimestamp}` : pic) : '';

  const handleTap = () => {
    if (entityId.startsWith('camera.')) refreshCamera(entityId);
    else if (card.camera_image) refreshCamera(card.camera_image);
    else void sendAction({ domain: getDomain(entityId), service: 'toggle', entity_id: entityId });
  };

  return (
    <div className="ha-card ha-card-picture" data-entity-id={entityId} data-domain={getDomain(entityId)} data-state={state} onClick={handleTap}>
      <div className="ha-picture-container">
        {imageSrc ? <img src={imageSrc} alt={name} className="ha-picture-img" loading="lazy" /> : <div className="ha-picture-fallback"><span>📹 {name}</span></div>}
        <div className="ha-picture-footer"><span className="ha-picture-name">{name}</span><span className="ha-picture-state">{state}</span></div>
      </div>
    </div>
  );
}

function renderLight(entityId: string, isOn: boolean, attrs: Record<string, unknown>, inFlight: boolean, sendAction: (p: ActionPayload) => Promise<void>) {
  const b = typeof attrs.brightness === 'number' ? Math.round((attrs.brightness / 255) * 100) : 0;
  return (
    <div className="ha-controls-light">
      <button type="button" className={`ha-btn ha-btn-toggle ${isOn ? 'is-on' : ''}`} disabled={inFlight} onClick={() => sendAction({ domain: 'light', service: 'toggle', entity_id: entityId })} data-action="toggle">{isOn ? 'Turn Off' : 'Turn On'}</button>
      {isOn && (
        <div className="ha-quick-brightness">
          {[25, 50, 100].map((pct) => (
            <button key={pct} type="button" className={`ha-btn-sub ${b >= pct - 10 && b <= pct + 10 ? 'active' : ''}`} disabled={inFlight} onClick={() => sendAction({ domain: 'light', service: 'turn_on', entity_id: entityId, service_data: { brightness: Math.round(pct * 2.55) } })}>{pct}%</button>
          ))}
        </div>
      )}
    </div>
  );
}

function renderFan(entityId: string, isOn: boolean, attrs: Record<string, unknown>, inFlight: boolean, sendAction: (p: ActionPayload) => Promise<void>) {
  const pct = typeof attrs.percentage === 'number' ? attrs.percentage : 0;
  return (
    <div className="ha-controls-fan">
      <button type="button" className={`ha-btn ha-btn-toggle ${isOn ? 'is-on' : ''}`} disabled={inFlight} onClick={() => sendAction({ domain: 'fan', service: 'toggle', entity_id: entityId })} data-action="toggle">{isOn ? 'Turn Off' : 'Turn On'}</button>
      {isOn && (
        <div className="ha-quick-speeds">
          {[33, 66, 100].map((spd) => (
            <button key={spd} type="button" className={`ha-btn-sub ${pct >= spd - 15 && pct <= spd + 15 ? 'active' : ''}`} disabled={inFlight} onClick={() => sendAction({ domain: 'fan', service: 'set_percentage', entity_id: entityId, service_data: { percentage: spd } })}>{spd === 33 ? 'Low' : spd === 66 ? 'Med' : 'High'}</button>
          ))}
        </div>
      )}
    </div>
  );
}

function renderCover(entityId: string, inFlight: boolean, sendAction: (p: ActionPayload) => Promise<void>) {
  return (
    <div className="ha-btn-group">
      {(['open_cover', 'stop_cover', 'close_cover'] as const).map((s) => (
        <button key={s} type="button" className="ha-btn ha-btn-cover" disabled={inFlight} onClick={() => sendAction({ domain: 'cover', service: s, entity_id: entityId })} data-action={s.split('_')[0]}>{s === 'open_cover' ? 'Open' : s === 'stop_cover' ? 'Stop' : 'Close'}</button>
      ))}
    </div>
  );
}

function renderMedia(entityId: string, isPlaying: boolean, inFlight: boolean, sendAction: (p: ActionPayload) => Promise<void>) {
  return (
    <div className="ha-btn-group ha-media-btn-group">
      <button type="button" className="ha-btn ha-btn-icon" disabled={inFlight} onClick={() => sendAction({ domain: 'media_player', service: 'media_previous_track', entity_id: entityId })} data-action="prev" title="Previous Track">⏮</button>
      <button type="button" className="ha-btn ha-btn-icon ha-btn-play" disabled={inFlight} onClick={() => sendAction({ domain: 'media_player', service: 'media_play_pause', entity_id: entityId })} data-action="play-pause" title={isPlaying ? 'Pause' : 'Play'}>{isPlaying ? '⏸' : '▶'}</button>
      <button type="button" className="ha-btn ha-btn-icon" disabled={inFlight} onClick={() => sendAction({ domain: 'media_player', service: 'media_next_track', entity_id: entityId })} data-action="next" title="Next Track">⏭</button>
      <button type="button" className="ha-btn ha-btn-icon" disabled={inFlight} onClick={() => sendAction({ domain: 'media_player', service: 'volume_down', entity_id: entityId })} data-action="vol-down" title="Volume Down">🔉</button>
      <button type="button" className="ha-btn ha-btn-icon" disabled={inFlight} onClick={() => sendAction({ domain: 'media_player', service: 'volume_up', entity_id: entityId })} data-action="vol-up" title="Volume Up">🔊</button>
    </div>
  );
}

const BINARY_LABELS: Record<string, [string, string]> = {
  door: ['Open', 'Closed'], window: ['Open', 'Closed'],
  occupancy: ['Occupied', 'Clear'], presence: ['Home', 'Away'], motion: ['Motion', 'Clear'],
};

export function getBinaryLabel(isOn: boolean, dc?: string): string {
  const pair = dc ? BINARY_LABELS[dc] : undefined;
  return pair ? (isOn ? pair[0] : pair[1]) : (isOn ? 'On' : 'Off');
}

function DomainTouchControls({ domain, entityId, entity, inFlight, sendAction, refreshCamera }: {
  domain: string; entityId: string; entity?: EntityState; inFlight: boolean;
  sendAction: (p: ActionPayload) => Promise<void>; refreshCamera: (id: string) => void;
}) {
  const state = entity?.state ?? 'off';
  const attrs = entity?.attributes || {};
  const isOn = state === 'on';
  const toggle = (dom = domain, srv = 'toggle') => sendAction({ domain: dom, service: srv, entity_id: entityId });

  switch (domain) {
    case 'light': return renderLight(entityId, isOn, attrs, inFlight, sendAction);
    case 'switch': return <button type="button" className={`ha-btn ha-btn-toggle ${isOn ? 'is-on' : ''}`} disabled={inFlight} onClick={() => toggle()} data-action="toggle">{isOn ? 'Turn Off' : 'Turn On'}</button>;
    case 'fan': return renderFan(entityId, isOn, attrs, inFlight, sendAction);
    case 'cover': return renderCover(entityId, inFlight, sendAction);
    case 'lock': {
      const isLocked = state === 'locked';
      return <button type="button" className={`ha-btn ${isLocked ? 'ha-btn-locked' : 'ha-btn-unlocked'}`} disabled={inFlight} onClick={() => sendAction({ domain: 'lock', service: isLocked ? 'unlock' : 'lock', entity_id: entityId })} data-action="toggle-lock">{isLocked ? 'Unlock' : 'Lock'}</button>;
    }
    case 'climate': {
      const targetTemp = (attrs.temperature as number | undefined) ?? 70;
      return (
        <div className="ha-btn-group">
          <button type="button" className="ha-btn ha-btn-temp" disabled={inFlight} onClick={() => sendAction({ domain: 'climate', service: 'set_temperature', entity_id: entityId, service_data: { temperature: targetTemp - 1 } })} data-action="temp-down">-</button>
          <span className="ha-temp-inline">{targetTemp}°</span>
          <button type="button" className="ha-btn ha-btn-temp" disabled={inFlight} onClick={() => sendAction({ domain: 'climate', service: 'set_temperature', entity_id: entityId, service_data: { temperature: targetTemp + 1 } })} data-action="temp-up">+</button>
        </div>
      );
    }
    case 'media_player': return renderMedia(entityId, state === 'playing', inFlight, sendAction);
    case 'camera': return <button type="button" className="ha-btn ha-btn-camera" disabled={inFlight} onClick={() => refreshCamera(entityId)} data-action="refresh">Refresh</button>;
    case 'scene':
    case 'script': return <button type="button" className="ha-btn ha-btn-primary" disabled={inFlight} onClick={() => toggle(domain, 'turn_on')} data-action={domain === 'scene' ? 'activate' : 'run'}>{domain === 'scene' ? 'Activate' : 'Run'}</button>;
    case 'sensor': return <div className="ha-sensor-pill" data-state={state}><span className="ha-sensor-val">{state}</span>{Boolean(attrs.unit_of_measurement) && <span className="ha-sensor-unit">{String(attrs.unit_of_measurement)}</span>}</div>;
    case 'binary_sensor': return <div className={`ha-binary-pill ${isOn ? 'is-active' : 'is-clear'}`} data-state={state}><span className="ha-pill-dot" /><span className="ha-pill-text">{getBinaryLabel(isOn, attrs.device_class as string | undefined)}</span></div>;
    default: return <div className="ha-generic-state">{state}</div>;
  }
}

export function formatStateLabel(domain: string, state: string, attrs?: Record<string, unknown>): string {
  if (domain === 'light') {
    const b = state === 'on' && typeof attrs?.brightness === 'number' ? Math.round((attrs.brightness / 255) * 100) : null;
    return b !== null ? `On · ${b}%` : state === 'on' ? 'On' : 'Off';
  }
  if (domain === 'climate' && attrs?.current_temperature !== undefined) return `${state} · ${attrs.current_temperature}°`;
  if (domain === 'media_player' && attrs?.media_title) {
    return attrs.media_artist ? `${attrs.media_title} - ${attrs.media_artist}` : String(attrs.media_title);
  }
  if (domain === 'sensor') return `${state} ${attrs?.unit_of_measurement || ''}`.trim();
  return state;
}
