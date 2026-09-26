/**
 * Mirrormere Realtime Event Client (SSE)
 * Connects to GET /api/events with auto-reconnection and exponential backoff.
 * Complies with SPEC-006: Event Streaming & Client Communication.
 */
class MirrormereSSE {
  constructor(url = '/api/events', options = {}) {
    this.url = url;
    this.options = options || {};
    this.eventSource = null;
    this.listeners = new Map();
    this.reconnectAttempts = 0;
    this.baseDelay = this.options.baseDelay || 1000;
    this.maxDelay = this.options.maxDelay || 30000;
    this.backoffFactor = this.options.backoffFactor || 1.5;
    this.jitter = typeof this.options.jitter === 'number' ? this.options.jitter : 500;
    this.reconnectTimer = null;
    this.isExplicitDisconnect = false;
    this.lastEventId = this.options.lastEventId || null;
    this._attachedEvents = new Set();

    // Extract initial lastEventId from URL query param if present and not explicitly provided
    if (!this.lastEventId && typeof url === 'string' && url.includes('lastEventId=')) {
      try {
        const dummyBase = 'http://mirrormere.local';
        const u = new URL(url, dummyBase);
        const qId = u.searchParams.get('lastEventId');
        if (qId && qId.trim().length > 0) {
          this.lastEventId = qId.trim();
        }
      } catch (_) {}
    }
  }

  /**
   * Helper to construct connection URL, appending or updating lastEventId query param if known.
   */
  getConnectionURL() {
    if (!this.lastEventId) {
      return this.url;
    }
    try {
      const isRelative = typeof this.url === 'string' && !/^https?:\/\//i.test(this.url);
      const dummyBase = (typeof window !== 'undefined' && window.location && window.location.origin)
        ? window.location.origin
        : 'http://mirrormere.local';
      const u = new URL(this.url, dummyBase);
      u.searchParams.set('lastEventId', this.lastEventId);
      if (isRelative) {
        const path = this.url.startsWith('/') ? u.pathname : u.pathname.replace(/^\//, '');
        return `${path}${u.search}${u.hash}`;
      }
      return u.toString();
    } catch (_) {
      const [pathAndQuery, hash] = this.url.split('#');
      const sep = pathAndQuery.includes('?') ? '&' : '?';
      return `${pathAndQuery}${sep}lastEventId=${encodeURIComponent(this.lastEventId)}${hash ? `#${hash}` : ''}`;
    }
  }

  /**
   * Record last event ID from SSE event or data payload.
   */
  _recordLastEventId(e, payload) {
    if (e && typeof e.lastEventId === 'string' && e.lastEventId.trim().length > 0) {
      this.lastEventId = e.lastEventId.trim();
    } else if (e && typeof e.id === 'string' && e.id.trim().length > 0) {
      this.lastEventId = e.id.trim();
    } else if (payload && typeof payload.id === 'string' && payload.id.trim().length > 0) {
      this.lastEventId = payload.id.trim();
    }
  }

  /**
   * Attach listener for specific event type to underlying EventSource.
   */
  _attachEventListener(evtName) {
    if (!this.eventSource || this._attachedEvents.has(evtName)) {
      return;
    }
    if (evtName === 'connection' || evtName === 'message') {
      return;
    }
    this._attachedEvents.add(evtName);
    this.eventSource.addEventListener(evtName, (e) => {
      let payload = e ? e.data : undefined;
      if (typeof e.data === 'string') {
        try {
          payload = JSON.parse(e.data);
        } catch (_) {
          // Keep raw text
        }
      }
      this._recordLastEventId(e, payload);
      this.emit(evtName, payload);
    });
  }

  /**
   * Register an event listener for SSE event types (e.g. 'screen.rotate', 'widget.update').
   */
  on(eventType, callback) {
    if (!this.listeners.has(eventType)) {
      this.listeners.set(eventType, new Set());
      if (this.eventSource) {
        this._attachEventListener(eventType);
      }
    }
    this.listeners.get(eventType).add(callback);
    return this;
  }

  /**
   * Remove an event listener.
   */
  off(eventType, callback) {
    if (this.listeners.has(eventType)) {
      this.listeners.get(eventType).delete(callback);
    }
    return this;
  }

  /**
   * Emit an event to registered local listeners.
   */
  emit(eventType, data) {
    if (this.listeners.has(eventType)) {
      for (const listener of this.listeners.get(eventType)) {
        try {
          listener(data);
        } catch (err) {
          console.error(`[MirrormereSSE] Listener error for event '${eventType}':`, err);
        }
      }
    }
  }

  /**
   * Initiate EventSource connection.
   */
  connect() {
    this.isExplicitDisconnect = false;
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    const targetUrl = this.getConnectionURL();
    const EventSourceConstructor = (this.options && this.options.EventSource) ||
      (typeof EventSource !== 'undefined' ? EventSource : (typeof window !== 'undefined' ? window.EventSource : null));

    if (!EventSourceConstructor) {
      console.error('[MirrormereSSE] EventSource is not available in environment');
      return;
    }

    try {
      this.eventSource = new EventSourceConstructor(targetUrl);
    } catch (err) {
      console.error('[MirrormereSSE] Failed to initialize EventSource:', err);
      this.scheduleReconnect();
      return;
    }

    this.eventSource.onopen = () => {
      this.reconnectAttempts = 0;
      this.emit('connection', {
        status: 'connected',
        lastEventId: this.lastEventId,
      });
    };

    this.eventSource.onerror = (err) => {
      this.emit('connection', {
        status: 'disconnected',
        error: err,
        lastEventId: this.lastEventId,
      });
      if (!this.isExplicitDisconnect) {
        this.scheduleReconnect();
      }
    };

    this._attachedEvents = new Set();

    // Standard SSE event types defined in SPEC-006
    const standardEvents = [
      'screen.rotate',
      'widget.update',
      'widget.reload',
      'style.reload',
      'header.update',
      'system.status',
      'video.state',
      'audio.state',
      'voice.state'
    ];

    const eventsToAttach = new Set([...standardEvents, ...this.listeners.keys()]);
    for (const evtName of eventsToAttach) {
      this._attachEventListener(evtName);
    }

    // Default message handler
    this.eventSource.onmessage = (e) => {
      let payload = e ? e.data : undefined;
      if (typeof e.data === 'string') {
        try {
          payload = JSON.parse(e.data);
        } catch (_) {
          // Raw message
        }
      }
      this._recordLastEventId(e, payload);
      this.emit('message', payload);
    };
  }

  /**
   * Schedule reconnection attempt with exponential backoff and jitter.
   */
  scheduleReconnect() {
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
    }

    const jitter = this.jitter > 0 ? (Math.random() * this.jitter) : 0;
    const delay = Math.min(
      this.maxDelay,
      this.baseDelay * Math.pow(this.backoffFactor, this.reconnectAttempts)
    ) + jitter;

    this.reconnectAttempts++;
    this.reconnectTimer = setTimeout(() => {
      if (!this.isExplicitDisconnect) {
        this.connect();
      }
    }, delay);
    if (this.reconnectTimer && typeof this.reconnectTimer.unref === 'function') {
      this.reconnectTimer.unref();
    }
  }

  /**
   * Disconnect and suspend auto-reconnection.
   */
  disconnect() {
    this.isExplicitDisconnect = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
    this.emit('connection', {
      status: 'disconnected',
      lastEventId: this.lastEventId,
    });
  }

  /**
   * Reset client state completely, clearing stored lastEventId and reconnect counters.
   */
  reset() {
    this.disconnect();
    this.lastEventId = null;
    this.reconnectAttempts = 0;
  }

  /**
   * Retrieve active lastEventId string.
   */
  getLastEventId() {
    return this.lastEventId;
  }

  /**
   * Explicitly set or override lastEventId.
   */
  setLastEventId(id) {
    this.lastEventId = (typeof id === 'string' && id.trim().length > 0) ? id.trim() : null;
  }
}

// Attach globally / export for testing
if (typeof window !== 'undefined') {
  window.MirrormereSSE = MirrormereSSE;
}
if (typeof module !== 'undefined' && module.exports) {
  module.exports = MirrormereSSE;
}
