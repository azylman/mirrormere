/**
 * Mirrormere Realtime Event Client (SSE)
 * Connects to GET /api/events with auto-reconnection and exponential backoff.
 * Complies with SPEC-006: Event Streaming & Client Communication.
 */
class MirrormereSSE {
  constructor(url = '/api/events') {
    this.url = url;
    this.eventSource = null;
    this.listeners = new Map();
    this.reconnectAttempts = 0;
    this.baseDelay = 1000;
    this.maxDelay = 30000;
    this.backoffFactor = 1.5;
    this.reconnectTimer = null;
    this.isExplicitDisconnect = false;
  }

  /**
   * Register an event listener for SSE event types (e.g. 'screen.rotate', 'widget.update').
   */
  on(eventType, callback) {
    if (!this.listeners.has(eventType)) {
      this.listeners.set(eventType, new Set());
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

    try {
      this.eventSource = new EventSource(this.url);
    } catch (err) {
      console.error('[MirrormereSSE] Failed to initialize EventSource:', err);
      this.scheduleReconnect();
      return;
    }

    this.eventSource.onopen = () => {
      this.reconnectAttempts = 0;
      this.emit('connection', { status: 'connected' });
    };

    this.eventSource.onerror = (err) => {
      this.emit('connection', { status: 'disconnected', error: err });
      if (!this.isExplicitDisconnect) {
        this.scheduleReconnect();
      }
    };

    // Standard SSE event types defined in SPEC-006
    const standardEvents = [
      'screen.rotate',
      'widget.update',
      'widget.reload',
      'style.reload',
      'header.update',
      'system.status',
      'voice.state'
    ];

    for (const evtName of standardEvents) {
      this.eventSource.addEventListener(evtName, (e) => {
        let payload = e.data;
        try {
          payload = JSON.parse(e.data);
        } catch (_) {
          // If not valid JSON, use raw text
        }
        this.emit(evtName, payload);
      });
    }

    // Default message handler
    this.eventSource.onmessage = (e) => {
      let payload = e.data;
      try {
        payload = JSON.parse(e.data);
      } catch (_) {
        // Raw message
      }
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

    const delay = Math.min(
      this.maxDelay,
      this.baseDelay * Math.pow(this.backoffFactor, this.reconnectAttempts)
    ) + (Math.random() * 500);

    this.reconnectAttempts++;
    this.reconnectTimer = setTimeout(() => {
      if (!this.isExplicitDisconnect) {
        this.connect();
      }
    }, delay);
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
    this.emit('connection', { status: 'disconnected' });
  }
}

// Attach globally
window.MirrormereSSE = MirrormereSSE;
