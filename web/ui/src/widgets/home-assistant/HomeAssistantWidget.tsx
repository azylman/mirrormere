import { memo, useCallback, useEffect, useMemo, useState } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';

export interface EntityState {
  entity_id: string;
  state: string;
  attributes: Record<string, unknown>;
  last_changed?: string;
  last_updated?: string;
}

interface LovelaceCard {
  type: string;
  title?: string;
  entity?: string;
  entities?: (string | { entity: string; name?: string; icon?: string; [key: string]: unknown })[];
  cards?: LovelaceCard[];
  sections?: LovelaceSection[];
  name?: string;
  icon?: string;
  image?: string;
  camera_image?: string;
  camera_view?: string;
  tap_action?: Record<string, unknown>;
  hold_action?: Record<string, unknown>;
  columns?: number;
  square?: boolean;
  [key: string]: unknown;
}

interface LovelaceSection {
  title?: string;
  cards?: LovelaceCard[];
  [key: string]: unknown;
}

interface LovelaceView {
  title?: string;
  path?: string;
  icon?: string;
  type?: string;
  cards?: LovelaceCard[];
  sections?: LovelaceSection[];
  [key: string]: unknown;
}

interface HomeAssistantSnapshot {
  title?: string;
  dashboard?: string;
  view?: LovelaceView;
  views?: LovelaceView[];
  cards?: LovelaceCard[];
  entities?: Record<string, EntityState>;
  discovered_entities?: string[];
  last_updated?: string;
}

export interface ActionPayload {
  domain: string;
  service: string;
  entity_id?: string;
  service_data?: Record<string, unknown>;
}

export function getDomain(entityId: string): string {
  if (!entityId) return '';
  const parts = entityId.split('.');
  return parts.length > 0 ? parts[0].toLowerCase() : '';
}

export function getEntityName(entity: EntityState | undefined, entityId: string, customName?: string): string {
  if (customName && customName.trim() !== '') return customName;
  if (entity?.attributes?.friendly_name && typeof entity.attributes.friendly_name === 'string') {
    return entity.attributes.friendly_name;
  }
  const parts = entityId.split('.');
  return parts.length > 1 ? parts[1].replace(/_/g, ' ') : entityId;
}

export function getDomainIcon(domain: string, state?: string): string {
  switch (domain) {
    case 'light':
      return state === 'on' ? '💡' : '○';
    case 'switch':
      return state === 'on' ? '🔌' : '○';
    case 'fan':
      return state === 'on' ? '🌀' : '🪭';
    case 'cover':
      return state === 'open' ? '🪟' : '🚪';
    case 'lock':
      return state === 'locked' ? '🔒' : '🔓';
    case 'climate':
      return '🌡️';
    case 'media_player':
      return state === 'playing' ? '🔊' : '🎵';
    case 'camera':
      return '📹';
    case 'scene':
      return '🎬';
    case 'script':
      return '⚡';
    case 'sensor':
      return '📊';
    case 'binary_sensor':
      return state === 'on' ? '🔔' : '🔕';
    default:
      return '⚙️';
  }
}

export async function dispatchWidgetAction(
  widgetId: string,
  payload: ActionPayload,
  fetchImpl: typeof fetch = fetch,
): Promise<unknown> {
  const url = `/api/widgets/${encodeURIComponent(widgetId)}/action`;
  const body: Record<string, unknown> = {
    domain: payload.domain,
    service: payload.service,
  };
  if (payload.entity_id) {
    body.entity_id = payload.entity_id;
  }
  if (payload.service_data) {
    body.service_data = {
      ...(payload.entity_id ? { entity_id: payload.entity_id } : {}),
      ...payload.service_data,
    };
  } else if (payload.entity_id) {
    body.service_data = { entity_id: payload.entity_id };
  }

  const res = await fetchImpl(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });

  if (!res.ok) {
    const errText = await res.text().catch(() => '');
    throw new Error(`Widget action failed (HTTP ${res.status}): ${errText}`);
  }

  return await res.json().catch(() => ({}));
}

export const HomeAssistantWidget = memo(function HomeAssistantWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const data = entry?.data as HomeAssistantSnapshot | undefined;
  const isError = entry?.state === 'error';

  const [localEntities, setLocalEntities] = useState<Record<string, EntityState>>({});
  const [inFlight, setInFlight] = useState<Record<string, boolean>>({});
  const [actionError, setActionError] = useState<string | null>(null);
  const [cameraTimestamps, setCameraTimestamps] = useState<Record<string, number>>({});

  useEffect(() => {
    if (data?.entities) {
      setLocalEntities(data.entities);
    }
  }, [data?.entities]);

  const sendAction = useCallback(
    async (payload: ActionPayload) => {
      const entId = payload.entity_id;
      if (entId) {
        setInFlight((prev) => ({ ...prev, [entId]: true }));
      }
      setActionError(null);

      // Optimistic update
      if (entId && localEntities[entId]) {
        const current = localEntities[entId];
        let nextState = current.state;
        const nextAttrs = { ...current.attributes };

        if (payload.domain === 'light' || payload.domain === 'switch' || payload.domain === 'fan') {
          if (payload.service === 'toggle') {
            nextState = current.state === 'on' ? 'off' : 'on';
          } else if (payload.service === 'turn_on') {
            nextState = 'on';
            if (payload.service_data?.brightness !== undefined) {
              nextAttrs.brightness = payload.service_data.brightness;
            }
            if (payload.service_data?.percentage !== undefined) {
              nextAttrs.percentage = payload.service_data.percentage;
            }
          } else if (payload.service === 'turn_off') {
            nextState = 'off';
          }
        } else if (payload.domain === 'lock') {
          if (payload.service === 'lock') nextState = 'locked';
          if (payload.service === 'unlock') nextState = 'unlocked';
        } else if (payload.domain === 'cover') {
          if (payload.service === 'open_cover') nextState = 'open';
          if (payload.service === 'close_cover') nextState = 'closed';
        } else if (payload.domain === 'climate') {
          if (payload.service === 'set_temperature' && payload.service_data?.temperature !== undefined) {
            nextAttrs.temperature = payload.service_data.temperature;
          }
          if (payload.service_data?.hvac_mode !== undefined) {
            nextState = String(payload.service_data.hvac_mode);
          }
        } else if (payload.domain === 'media_player') {
          if (payload.service === 'media_play_pause') {
            nextState = current.state === 'playing' ? 'paused' : 'playing';
          } else if (payload.service === 'media_play') {
            nextState = 'playing';
          } else if (payload.service === 'media_pause') {
            nextState = 'paused';
          }
        }

        setLocalEntities((prev) => ({
          ...prev,
          [entId]: { ...current, state: nextState, attributes: nextAttrs },
        }));
      }

      try {
        await dispatchWidgetAction(id, payload);
      } catch (err) {
        // Rollback on error if needed
        if (data?.entities) {
          setLocalEntities(data.entities);
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
    },
    [id, localEntities, data?.entities],
  );

  const refreshCamera = useCallback((entityId: string) => {
    setCameraTimestamps((prev) => ({ ...prev, [entityId]: Date.now() }));
  }, []);

  const title = data?.title || data?.view?.title || 'Home Assistant';

  const cardsToRender = useMemo(() => {
    if (data?.view?.sections && data.view.sections.length > 0) {
      return null;
    }
    if (data?.cards && data.cards.length > 0) {
      return data.cards;
    }
    if (data?.view?.cards && data.view.cards.length > 0) {
      return data.view.cards;
    }
    if (localEntities && Object.keys(localEntities).length > 0) {
      return Object.keys(localEntities).map((entId) => {
        const dom = getDomain(entId);
        if (dom === 'climate') return { type: 'thermostat', entity: entId };
        if (dom === 'camera') return { type: 'picture-entity', entity: entId };
        if (dom === 'scene' || dom === 'script') return { type: 'button', entity: entId };
        return { type: 'tile', entity: entId };
      });
    }
    return [];
  }, [data?.view?.sections, data?.cards, data?.view?.cards, localEntities]);

  if (isError) {
    return (
      <div className="widget-home-assistant widget-ha-error" data-widget-type="home-assistant">
        <div className="ha-error-message">Unable to load Home Assistant dashboard.</div>
      </div>
    );
  }

  if (!data || (!data.cards && !data.view && Object.keys(localEntities).length === 0)) {
    return (
      <div className="widget-home-assistant widget-ha-loading" data-widget-type="home-assistant">
        <div className="ha-loading-spinner">Loading Home Assistant...</div>
      </div>
    );
  }

  return (
    <div className="widget-home-assistant" data-widget-type="home-assistant" data-widget-id={id}>
      <header className="ha-header">
        <div className="ha-title-wrap">
          <span className="ha-icon">🏠</span>
          <h2 className="ha-title">{title}</h2>
        </div>
        {data.last_updated && (
          <span className="ha-last-updated" title={data.last_updated}>
            {new Date(data.last_updated).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </span>
        )}
      </header>

      {actionError && (
        <div className="ha-alert-banner" role="alert">
          <span>⚠️ {actionError}</span>
          <button type="button" className="ha-banner-dismiss" onClick={() => setActionError(null)}>
            ✕
          </button>
        </div>
      )}

      <main className="ha-content">
        {data.view?.sections && data.view.sections.length > 0 ? (
          <div className="ha-sections-layout">
            {data.view.sections.map((section, sIdx) => (
              <section key={`sec-${sIdx}`} className="ha-section" data-section-index={sIdx}>
                {section.title && <h3 className="ha-section-title">{section.title}</h3>}
                <div className="ha-cards-grid">
                  {(section.cards || []).map((card, cIdx) => (
                    <CardRenderer
                      key={`card-${sIdx}-${cIdx}`}
                      card={card}
                      entities={localEntities}
                      inFlight={inFlight}
                      sendAction={sendAction}
                      refreshCamera={refreshCamera}
                      cameraTimestamps={cameraTimestamps}
                    />
                  ))}
                </div>
              </section>
            ))}
          </div>
        ) : (
          <div className="ha-cards-grid">
            {(cardsToRender || []).map((card, cIdx) => (
              <CardRenderer
                key={`card-${cIdx}`}
                card={card}
                entities={localEntities}
                inFlight={inFlight}
                sendAction={sendAction}
                refreshCamera={refreshCamera}
                cameraTimestamps={cameraTimestamps}
              />
            ))}
          </div>
        )}
      </main>
    </div>
  );
});

interface CardRendererProps {
  card: LovelaceCard;
  entities: Record<string, EntityState>;
  inFlight: Record<string, boolean>;
  sendAction: (payload: ActionPayload) => Promise<void>;
  refreshCamera: (entityId: string) => void;
  cameraTimestamps: Record<string, number>;
}

function CardRenderer({
  card,
  entities,
  inFlight,
  sendAction,
  refreshCamera,
  cameraTimestamps,
}: CardRendererProps) {
  const cardType = (card.type || '').toLowerCase();

  if (cardType === 'grid' && card.cards) {
    const cols = card.columns || 2;
    return (
      <div className="ha-card ha-card-grid" style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))` }}>
        {card.cards.map((subCard, idx) => (
          <CardRenderer
            key={`sub-${idx}`}
            card={subCard}
            entities={entities}
            inFlight={inFlight}
            sendAction={sendAction}
            refreshCamera={refreshCamera}
            cameraTimestamps={cameraTimestamps}
          />
        ))}
      </div>
    );
  }

  if (cardType === 'entities') {
    const entityList = card.entities || [];
    return (
      <div className="ha-card ha-card-entities">
        {card.title && <h4 className="ha-card-title">{card.title}</h4>}
        <div className="ha-entity-list">
          {entityList.map((item, idx) => {
            const entId = typeof item === 'string' ? item : item.entity;
            const customName = typeof item === 'object' && item !== null ? item.name : undefined;
            const entity = entities[entId];
            return (
              <EntityControlRow
                key={`ent-${idx}-${entId}`}
                entityId={entId}
                entity={entity}
                customName={customName}
                inFlight={Boolean(inFlight[entId])}
                sendAction={sendAction}
                refreshCamera={refreshCamera}
                cameraTimestamps={cameraTimestamps}
              />
            );
          })}
        </div>
      </div>
    );
  }

  if (cardType === 'thermostat') {
    const entId = card.entity || '';
    const entity = entities[entId];
    return (
      <ThermostatCard
        entityId={entId}
        entity={entity}
        title={card.title}
        inFlight={Boolean(inFlight[entId])}
        sendAction={sendAction}
      />
    );
  }

  if (cardType === 'picture-entity') {
    const entId = card.entity || '';
    const entity = entities[entId];
    return (
      <PictureEntityCard
        card={card}
        entityId={entId}
        entity={entity}
        refreshCamera={refreshCamera}
        cameraTimestamp={cameraTimestamps[entId]}
        sendAction={sendAction}
      />
    );
  }

  if (cardType === 'button') {
    const entId = card.entity || '';
    const entity = entities[entId];
    return (
      <ButtonCard
        card={card}
        entityId={entId}
        entity={entity}
        inFlight={Boolean(inFlight[entId])}
        sendAction={sendAction}
      />
    );
  }

  // Tile or default entity card
  const entId = card.entity || '';
  if (entId) {
    const entity = entities[entId];
    return (
      <TileCard
        entityId={entId}
        entity={entity}
        card={card}
        inFlight={Boolean(inFlight[entId])}
        sendAction={sendAction}
        refreshCamera={refreshCamera}
        cameraTimestamps={cameraTimestamps}
      />
    );
  }

  if (card.cards && card.cards.length > 0) {
    return (
      <div className="ha-card ha-card-wrapper">
        {card.title && <h4 className="ha-card-title">{card.title}</h4>}
        {card.cards.map((c, i) => (
          <CardRenderer
            key={`nested-${i}`}
            card={c}
            entities={entities}
            inFlight={inFlight}
            sendAction={sendAction}
            refreshCamera={refreshCamera}
            cameraTimestamps={cameraTimestamps}
          />
        ))}
      </div>
    );
  }

  return (
    <div className="ha-card ha-card-unknown">
      {card.title || card.name || cardType || 'Card'}
    </div>
  );
}

interface EntityControlRowProps {
  entityId: string;
  entity?: EntityState;
  customName?: string;
  inFlight: boolean;
  sendAction: (payload: ActionPayload) => Promise<void>;
  refreshCamera: (entityId: string) => void;
  cameraTimestamps: Record<string, number>;
}

function EntityControlRow({
  entityId,
  entity,
  customName,
  inFlight,
  sendAction,
  refreshCamera,
}: EntityControlRowProps) {
  const domain = getDomain(entityId);
  const name = getEntityName(entity, entityId, customName);
  const state = entity?.state ?? 'unavailable';
  const icon = getDomainIcon(domain, state);

  return (
    <div
      className={`ha-entity-row ha-domain-${domain} ${state === 'on' ? 'is-active' : ''}`}
      data-entity-id={entityId}
      data-domain={domain}
      data-state={state}
    >
      <div className="ha-row-left">
        <span className="ha-row-icon">{icon}</span>
        <div className="ha-row-meta">
          <div className="ha-row-name">{name}</div>
          <div className="ha-row-state-label">
            {formatStateLabel(domain, state, entity?.attributes)}
          </div>
        </div>
      </div>
      <div className="ha-row-actions" data-prevent-screen-swipe="">
        <DomainTouchControls
          domain={domain}
          entityId={entityId}
          entity={entity}
          inFlight={inFlight}
          sendAction={sendAction}
          refreshCamera={refreshCamera}
        />
      </div>
    </div>
  );
}

function TileCard({
  entityId,
  entity,
  card,
  inFlight,
  sendAction,
  refreshCamera,
}: {
  entityId: string;
  entity?: EntityState;
  card: LovelaceCard;
  inFlight: boolean;
  sendAction: (payload: ActionPayload) => Promise<void>;
  refreshCamera: (entityId: string) => void;
  cameraTimestamps: Record<string, number>;
}) {
  const domain = getDomain(entityId);
  const name = getEntityName(entity, entityId, card.name || card.title);
  const state = entity?.state ?? 'unavailable';
  const icon = card.icon || getDomainIcon(domain, state);

  return (
    <div
      className={`ha-card ha-card-tile ha-domain-${domain} ${state === 'on' ? 'is-active' : ''}`}
      data-entity-id={entityId}
      data-domain={domain}
      data-state={state}
    >
      <div className="ha-tile-header">
        <span className="ha-tile-icon">{icon}</span>
        <div className="ha-tile-meta">
          <div className="ha-tile-name">{name}</div>
          <div className="ha-tile-state">{formatStateLabel(domain, state, entity?.attributes)}</div>
        </div>
      </div>
      <div className="ha-tile-controls" data-prevent-screen-swipe="">
        <DomainTouchControls
          domain={domain}
          entityId={entityId}
          entity={entity}
          inFlight={inFlight}
          sendAction={sendAction}
          refreshCamera={refreshCamera}
        />
      </div>
    </div>
  );
}

function ButtonCard({
  card,
  entityId,
  entity,
  inFlight,
  sendAction,
}: {
  card: LovelaceCard;
  entityId: string;
  entity?: EntityState;
  inFlight: boolean;
  sendAction: (payload: ActionPayload) => Promise<void>;
}) {
  const domain = getDomain(entityId);
  const name = getEntityName(entity, entityId, card.name || card.title);
  const state = entity?.state ?? 'idle';
  const icon = card.icon || getDomainIcon(domain, state);

  const handleTap = () => {
    if (inFlight) return;
    if (card.tap_action) {
      const act = card.tap_action;
      if (act.action === 'call-service' || act.action === 'call_service') {
        const serviceFull = String(act.service || '');
        const [dom, srv] = serviceFull.includes('.') ? serviceFull.split('.') : [domain, serviceFull];
        const actTarget = act.target as Record<string, unknown> | undefined;
        const actSvcData = act.service_data as Record<string, unknown> | undefined;
        const targetEnt = typeof actTarget?.['entity_id'] === 'string' ? (actTarget['entity_id'] as string) : undefined;
        const svcEnt = typeof actSvcData?.['entity_id'] === 'string' ? (actSvcData['entity_id'] as string) : undefined;
        void sendAction({
          domain: dom || domain,
          service: srv,
          entity_id: targetEnt || svcEnt || entityId,
          service_data: (act.service_data || act.data || {}) as Record<string, unknown>,
        });
        return;
      }
    }
    // Default action by domain
    if (domain === 'scene') {
      void sendAction({ domain: 'scene', service: 'turn_on', entity_id: entityId });
    } else if (domain === 'script') {
      void sendAction({ domain: 'script', service: 'turn_on', entity_id: entityId });
    } else if (domain === 'light' || domain === 'switch' || domain === 'fan') {
      void sendAction({ domain, service: 'toggle', entity_id: entityId });
    } else if (domain === 'lock') {
      const srv = state === 'locked' ? 'unlock' : 'lock';
      void sendAction({ domain: 'lock', service: srv, entity_id: entityId });
    } else if (domain) {
      void sendAction({ domain, service: 'toggle', entity_id: entityId });
    }
  };

  return (
    <button
      type="button"
      className={`ha-card ha-card-button ha-btn-touch ha-domain-${domain} ${state === 'on' ? 'is-active' : ''}`}
      data-entity-id={entityId}
      data-domain={domain}
      data-state={state}
      data-prevent-screen-swipe=""
      disabled={inFlight}
      onClick={handleTap}
    >
      <span className="ha-btn-card-icon">{icon}</span>
      <span className="ha-btn-card-name">{name}</span>
      <span className="ha-btn-card-state">{state}</span>
    </button>
  );
}

function ThermostatCard({
  entityId,
  entity,
  title,
  inFlight,
  sendAction,
}: {
  entityId: string;
  entity?: EntityState;
  title?: string;
  inFlight: boolean;
  sendAction: (payload: ActionPayload) => Promise<void>;
}) {
  const name = title || getEntityName(entity, entityId);
  const state = entity?.state ?? 'off';
  const currentTemp = entity?.attributes?.current_temperature as number | undefined;
  const targetTemp = (entity?.attributes?.temperature as number | undefined) ?? 70;
  const modes = (entity?.attributes?.hvac_modes as string[] | undefined) || ['off', 'heat', 'cool', 'auto'];

  const setTemp = (newTemp: number) => {
    void sendAction({
      domain: 'climate',
      service: 'set_temperature',
      entity_id: entityId,
      service_data: { temperature: newTemp },
    });
  };

  const setMode = (mode: string) => {
    void sendAction({
      domain: 'climate',
      service: 'set_hvac_mode',
      entity_id: entityId,
      service_data: { hvac_mode: mode },
    });
  };

  return (
    <div
      className={`ha-card ha-card-thermostat is-${state}`}
      data-entity-id={entityId}
      data-domain="climate"
      data-state={state}
    >
      <div className="ha-thermostat-header">
        <span className="ha-thermostat-icon">🌡️</span>
        <div className="ha-thermostat-title">{name}</div>
        <div className="ha-thermostat-state-badge">{state.toUpperCase()}</div>
      </div>
      <div className="ha-thermostat-body">
        {currentTemp !== undefined && (
          <div className="ha-current-temp" title="Current Temperature">
            Current: <span className="ha-temp-val">{currentTemp}°</span>
          </div>
        )}
        <div className="ha-target-temp-dial" data-prevent-screen-swipe="">
          <button
            type="button"
            className="ha-btn-circle"
            disabled={inFlight}
            onClick={() => setTemp(targetTemp - 1)}
            aria-label="Decrease Temperature"
          >
            -
          </button>
          <div className="ha-target-display">
            <span className="ha-target-num">{targetTemp}°</span>
            <span className="ha-target-lbl">Target</span>
          </div>
          <button
            type="button"
            className="ha-btn-circle"
            disabled={inFlight}
            onClick={() => setTemp(targetTemp + 1)}
            aria-label="Increase Temperature"
          >
            +
          </button>
        </div>
      </div>
      <div className="ha-thermostat-modes" data-prevent-screen-swipe="">
        {modes.map((mode) => (
          <button
            key={mode}
            type="button"
            className={`ha-btn-mode ${state === mode ? 'active' : ''}`}
            disabled={inFlight}
            onClick={() => setMode(mode)}
          >
            {mode}
          </button>
        ))}
      </div>
    </div>
  );
}

function PictureEntityCard({
  card,
  entityId,
  entity,
  refreshCamera,
  cameraTimestamp,
  sendAction,
}: {
  card: LovelaceCard;
  entityId: string;
  entity?: EntityState;
  refreshCamera: (entityId: string) => void;
  cameraTimestamp?: number;
  sendAction: (payload: ActionPayload) => Promise<void>;
}) {
  const name = getEntityName(entity, entityId, card.title || card.name);
  const state = entity?.state ?? 'idle';
  const pic = (entity?.attributes?.entity_picture as string | undefined) || card.image || card.camera_image;
  const imageSrc = pic ? (cameraTimestamp ? `${pic}?ts=${cameraTimestamp}` : pic) : '';

  const handleTap = () => {
    if (entityId.startsWith('camera.')) {
      refreshCamera(entityId);
    } else {
      void sendAction({ domain: getDomain(entityId), service: 'toggle', entity_id: entityId });
    }
  };

  return (
    <div
      className="ha-card ha-card-picture"
      data-entity-id={entityId}
      data-domain={getDomain(entityId)}
      data-state={state}
      onClick={handleTap}
    >
      <div className="ha-picture-container">
        {imageSrc ? (
          <img src={imageSrc} alt={name} className="ha-picture-img" loading="lazy" />
        ) : (
          <div className="ha-picture-fallback">
            <span>📹 {name}</span>
          </div>
        )}
        <div className="ha-picture-footer">
          <span className="ha-picture-name">{name}</span>
          <span className="ha-picture-state">{state}</span>
        </div>
      </div>
    </div>
  );
}

interface DomainTouchControlsProps {
  domain: string;
  entityId: string;
  entity?: EntityState;
  inFlight: boolean;
  sendAction: (payload: ActionPayload) => Promise<void>;
  refreshCamera: (entityId: string) => void;
}

function DomainTouchControls({
  domain,
  entityId,
  entity,
  inFlight,
  sendAction,
  refreshCamera,
}: DomainTouchControlsProps) {
  const state = entity?.state ?? 'off';
  const attrs = entity?.attributes || {};

  switch (domain) {
    case 'light': {
      const isOn = state === 'on';
      const brightness = typeof attrs.brightness === 'number' ? Math.round((attrs.brightness / 255) * 100) : 0;
      return (
        <div className="ha-controls-light">
          <button
            type="button"
            className={`ha-btn ha-btn-toggle ${isOn ? 'is-on' : ''}`}
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'light', service: 'toggle', entity_id: entityId })}
            data-action="toggle"
          >
            {isOn ? 'Turn Off' : 'Turn On'}
          </button>
          {isOn && (
            <div className="ha-quick-brightness">
              {[25, 50, 100].map((pct) => (
                <button
                  key={pct}
                  type="button"
                  className={`ha-btn-sub ${brightness >= pct - 10 && brightness <= pct + 10 ? 'active' : ''}`}
                  disabled={inFlight}
                  onClick={() =>
                    sendAction({
                      domain: 'light',
                      service: 'turn_on',
                      entity_id: entityId,
                      service_data: { brightness: Math.round(pct * 2.55) },
                    })
                  }
                >
                  {pct}%
                </button>
              ))}
            </div>
          )}
        </div>
      );
    }

    case 'switch': {
      const isOn = state === 'on';
      return (
        <button
          type="button"
          className={`ha-btn ha-btn-toggle ${isOn ? 'is-on' : ''}`}
          disabled={inFlight}
          onClick={() => sendAction({ domain: 'switch', service: 'toggle', entity_id: entityId })}
          data-action="toggle"
        >
          {isOn ? 'Turn Off' : 'Turn On'}
        </button>
      );
    }

    case 'fan': {
      const isOn = state === 'on';
      const pct = typeof attrs.percentage === 'number' ? attrs.percentage : 0;
      return (
        <div className="ha-controls-fan">
          <button
            type="button"
            className={`ha-btn ha-btn-toggle ${isOn ? 'is-on' : ''}`}
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'fan', service: 'toggle', entity_id: entityId })}
            data-action="toggle"
          >
            {isOn ? 'Turn Off' : 'Turn On'}
          </button>
          {isOn && (
            <div className="ha-quick-speeds">
              {[33, 66, 100].map((speed) => (
                <button
                  key={speed}
                  type="button"
                  className={`ha-btn-sub ${pct >= speed - 15 && pct <= speed + 15 ? 'active' : ''}`}
                  disabled={inFlight}
                  onClick={() =>
                    sendAction({
                      domain: 'fan',
                      service: 'set_percentage',
                      entity_id: entityId,
                      service_data: { percentage: speed },
                    })
                  }
                >
                  {speed === 33 ? 'Low' : speed === 66 ? 'Med' : 'High'}
                </button>
              ))}
            </div>
          )}
        </div>
      );
    }

    case 'cover': {
      return (
        <div className="ha-btn-group">
          <button
            type="button"
            className="ha-btn ha-btn-cover"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'cover', service: 'open_cover', entity_id: entityId })}
            data-action="open"
          >
            Open
          </button>
          <button
            type="button"
            className="ha-btn ha-btn-cover"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'cover', service: 'stop_cover', entity_id: entityId })}
            data-action="stop"
          >
            Stop
          </button>
          <button
            type="button"
            className="ha-btn ha-btn-cover"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'cover', service: 'close_cover', entity_id: entityId })}
            data-action="close"
          >
            Close
          </button>
        </div>
      );
    }

    case 'lock': {
      const isLocked = state === 'locked';
      return (
        <button
          type="button"
          className={`ha-btn ${isLocked ? 'ha-btn-locked' : 'ha-btn-unlocked'}`}
          disabled={inFlight}
          onClick={() =>
            sendAction({
              domain: 'lock',
              service: isLocked ? 'unlock' : 'lock',
              entity_id: entityId,
            })
          }
          data-action="toggle-lock"
        >
          {isLocked ? 'Unlock' : 'Lock'}
        </button>
      );
    }

    case 'climate': {
      const targetTemp = (attrs.temperature as number | undefined) ?? 70;
      return (
        <div className="ha-btn-group">
          <button
            type="button"
            className="ha-btn ha-btn-temp"
            disabled={inFlight}
            onClick={() =>
              sendAction({
                domain: 'climate',
                service: 'set_temperature',
                entity_id: entityId,
                service_data: { temperature: targetTemp - 1 },
              })
            }
            data-action="temp-down"
          >
            -
          </button>
          <span className="ha-temp-inline">{targetTemp}°</span>
          <button
            type="button"
            className="ha-btn ha-btn-temp"
            disabled={inFlight}
            onClick={() =>
              sendAction({
                domain: 'climate',
                service: 'set_temperature',
                entity_id: entityId,
                service_data: { temperature: targetTemp + 1 },
              })
            }
            data-action="temp-up"
          >
            +
          </button>
        </div>
      );
    }

    case 'media_player': {
      const isPlaying = state === 'playing';
      return (
        <div className="ha-btn-group ha-media-btn-group">
          <button
            type="button"
            className="ha-btn ha-btn-icon"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'media_player', service: 'media_previous_track', entity_id: entityId })}
            data-action="prev"
            title="Previous Track"
          >
            ⏮
          </button>
          <button
            type="button"
            className="ha-btn ha-btn-icon ha-btn-play"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'media_player', service: 'media_play_pause', entity_id: entityId })}
            data-action="play-pause"
            title={isPlaying ? 'Pause' : 'Play'}
          >
            {isPlaying ? '⏸' : '▶'}
          </button>
          <button
            type="button"
            className="ha-btn ha-btn-icon"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'media_player', service: 'media_next_track', entity_id: entityId })}
            data-action="next"
            title="Next Track"
          >
            ⏭
          </button>
          <button
            type="button"
            className="ha-btn ha-btn-icon"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'media_player', service: 'volume_down', entity_id: entityId })}
            data-action="vol-down"
            title="Volume Down"
          >
            🔉
          </button>
          <button
            type="button"
            className="ha-btn ha-btn-icon"
            disabled={inFlight}
            onClick={() => sendAction({ domain: 'media_player', service: 'volume_up', entity_id: entityId })}
            data-action="vol-up"
            title="Volume Up"
          >
            🔊
          </button>
        </div>
      );
    }

    case 'camera': {
      return (
        <button
          type="button"
          className="ha-btn ha-btn-camera"
          disabled={inFlight}
          onClick={() => refreshCamera(entityId)}
          data-action="refresh"
        >
          Refresh
        </button>
      );
    }

    case 'scene': {
      return (
        <button
          type="button"
          className="ha-btn ha-btn-primary"
          disabled={inFlight}
          onClick={() => sendAction({ domain: 'scene', service: 'turn_on', entity_id: entityId })}
          data-action="activate"
        >
          Activate
        </button>
      );
    }

    case 'script': {
      return (
        <button
          type="button"
          className="ha-btn ha-btn-primary"
          disabled={inFlight}
          onClick={() => sendAction({ domain: 'script', service: 'turn_on', entity_id: entityId })}
          data-action="run"
        >
          Run
        </button>
      );
    }

    case 'sensor': {
      const unit = (attrs.unit_of_measurement as string | undefined) || '';
      return (
        <div className="ha-sensor-pill" data-state={state}>
          <span className="ha-sensor-val">{state}</span>
          {unit && <span className="ha-sensor-unit">{unit}</span>}
        </div>
      );
    }

    case 'binary_sensor': {
      const isOn = state === 'on';
      const devClass = attrs.device_class as string | undefined;
      let label = isOn ? 'On' : 'Off';
      if (devClass === 'motion') label = isOn ? 'Motion' : 'Clear';
      else if (devClass === 'door' || devClass === 'window') label = isOn ? 'Open' : 'Closed';
      else if (devClass === 'occupancy') label = isOn ? 'Occupied' : 'Clear';
      else if (devClass === 'presence') label = isOn ? 'Home' : 'Away';

      return (
        <div className={`ha-binary-pill ${isOn ? 'is-active' : 'is-clear'}`} data-state={state}>
          <span className="ha-pill-dot" />
          <span className="ha-pill-text">{label}</span>
        </div>
      );
    }

    default:
      return <div className="ha-generic-state">{state}</div>;
  }
}

function formatStateLabel(domain: string, state: string, attrs: Record<string, unknown> | undefined): string {
  if (domain === 'light') {
    if (state === 'on') {
      const b = typeof attrs?.brightness === 'number' ? Math.round((attrs.brightness / 255) * 100) : null;
      return b !== null ? `On · ${b}%` : 'On';
    }
    return 'Off';
  }
  if (domain === 'climate') {
    const cur = attrs?.current_temperature;
    return cur !== undefined ? `${state} · ${cur}°` : state;
  }
  if (domain === 'media_player') {
    const title = attrs?.media_title;
    const artist = attrs?.media_artist;
    if (title && artist) return `${title} - ${artist}`;
    if (title) return String(title);
    return state;
  }
  if (domain === 'sensor') {
    const unit = attrs?.unit_of_measurement || '';
    return `${state} ${unit}`.trim();
  }
  return state;
}
