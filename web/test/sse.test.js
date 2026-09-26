const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const MirrormereSSE = require('../static/js/sse.js');

describe('MirrormereSSE Client & Replay Resumption (SPEC-006 §1, §3)', () => {
  describe('Connection URL Construction (getConnectionURL)', () => {
    test('defaults to relative api/events URL when omitted', () => {
      const sse = new MirrormereSSE();
      assert.equal(sse.getConnectionURL(), 'api/events');
    });

    test('returns base URL unmodified when lastEventId is null or empty', () => {
      const sse = new MirrormereSSE('/api/events');
      assert.equal(sse.getConnectionURL(), '/api/events');

      sse.setLastEventId('   ');
      assert.equal(sse.getConnectionURL(), '/api/events');
    });

    test('appends lastEventId query parameter to clean URL', () => {
      const sse = new MirrormereSSE('/api/events', { lastEventId: 'evt-1001' });
      assert.equal(sse.getConnectionURL(), '/api/events?lastEventId=evt-1001');
    });

    test('appends lastEventId preserving existing query parameters', () => {
      const sse = new MirrormereSSE('/api/events?profile=touch&screen=1', { lastEventId: 'evt-2002' });
      const url = sse.getConnectionURL();
      assert.ok(url.startsWith('/api/events?'));
      assert.ok(url.includes('profile=touch'));
      assert.ok(url.includes('screen=1'));
      assert.ok(url.includes('lastEventId=evt-2002'));
    });

    test('updates existing lastEventId in query parameters without duplicating', () => {
      const sse = new MirrormereSSE('/api/events?lastEventId=old-id&screen=2');
      assert.equal(sse.getLastEventId(), 'old-id');

      sse.setLastEventId('new-id');
      const url = sse.getConnectionURL();
      assert.ok(!url.includes('old-id'));
      assert.ok(url.includes('lastEventId=new-id'));
      assert.ok(url.includes('screen=2'));
      // Count occurrences of lastEventId
      const matches = url.match(/lastEventId=/g);
      assert.equal(matches.length, 1);
    });

    test('preserves hash fragments and relative URL formatting', () => {
      const sse = new MirrormereSSE('/api/events#section-1', { lastEventId: 'evt-hash' });
      assert.equal(sse.getConnectionURL(), '/api/events?lastEventId=evt-hash#section-1');

      const relativeSse = new MirrormereSSE('api/events#hash', { lastEventId: 'evt-rel' });
      assert.equal(relativeSse.getConnectionURL(), 'api/events?lastEventId=evt-rel#hash');
    });

    test('preserves absolute HTTP/HTTPS URLs', () => {
      const sse = new MirrormereSSE('http://127.0.0.1:8080/api/events?kiosk=1', { lastEventId: 'evt-abs' });
      assert.equal(sse.getConnectionURL(), 'http://127.0.0.1:8080/api/events?kiosk=1&lastEventId=evt-abs');
    });
  });

  describe('Mock EventSource & Replay Event Stamping', () => {
    class MockEventSource {
      constructor(url) {
        this.url = url;
        this.readyState = 0; // CONNECTING
        this.listeners = new Map();
        this.onopen = null;
        this.onerror = null;
        this.onmessage = null;
        MockEventSource.instances.push(this);
      }

      addEventListener(type, cb) {
        if (!this.listeners.has(type)) {
          this.listeners.set(type, new Set());
        }
        this.listeners.get(type).add(cb);
      }

      removeEventListener(type, cb) {
        if (this.listeners.has(type)) {
          this.listeners.get(type).delete(cb);
        }
      }

      simulateOpen() {
        this.readyState = 1; // OPEN
        if (typeof this.onopen === 'function') {
          this.onopen({});
        }
      }

      simulateError(err = new Error('Connection lost')) {
        if (typeof this.onerror === 'function') {
          this.onerror(err);
        }
      }

      simulateEvent(type, data, lastEventId) {
        const evt = {
          type,
          data: typeof data === 'string' ? data : JSON.stringify(data),
          lastEventId,
        };

        if (type === 'message' && typeof this.onmessage === 'function') {
          this.onmessage(evt);
        }

        if (this.listeners.has(type)) {
          for (const cb of this.listeners.get(type)) {
            cb(evt);
          }
        }
      }

      close() {
        this.readyState = 2; // CLOSED
      }
    }
    MockEventSource.instances = [];

    test('stamps lastEventId from standard SSE events and generic messages', () => {
      MockEventSource.instances = [];
      const sse = new MirrormereSSE('/api/events', { EventSource: MockEventSource });

      let receivedWidgetData = null;
      let receivedRotateData = null;
      let receivedMsgData = null;

      sse.on('widget.update', (data) => { receivedWidgetData = data; });
      sse.on('screen.rotate', (data) => { receivedRotateData = data; });
      sse.on('message', (data) => { receivedMsgData = data; });

      sse.connect();
      assert.equal(MockEventSource.instances.length, 1);
      const es = MockEventSource.instances[0];
      es.simulateOpen();

      assert.equal(sse.getLastEventId(), null);

      // 1. Deliver screen.rotate with ID
      es.simulateEvent('screen.rotate', { current_screen: 0, total_screens: 2 }, 'evt-rotate-001');
      assert.equal(sse.getLastEventId(), 'evt-rotate-001');
      assert.deepEqual(receivedRotateData, { current_screen: 0, total_screens: 2 });

      // 2. Deliver widget.update with next ID
      es.simulateEvent('widget.update', { widget_id: 'weather', state: 'healthy' }, 'evt-widget-002');
      assert.equal(sse.getLastEventId(), 'evt-widget-002');
      assert.deepEqual(receivedWidgetData, { widget_id: 'weather', state: 'healthy' });

      // 3. Deliver default message with another ID
      es.simulateEvent('message', { hello: 'world' }, 'evt-msg-003');
      assert.equal(sse.getLastEventId(), 'evt-msg-003');
      assert.deepEqual(receivedMsgData, { hello: 'world' });
    });

    test('reconnects with ?lastEventId=<id> upon connection drop', async () => {
      MockEventSource.instances = [];
      const sse = new MirrormereSSE('/api/events', {
        EventSource: MockEventSource,
        baseDelay: 10,
        maxDelay: 50,
        jitter: 0,
      });

      const connectionStates = [];
      sse.on('connection', (c) => connectionStates.push(c));

      sse.connect();
      assert.equal(MockEventSource.instances.length, 1);
      let es1 = MockEventSource.instances[0];
      assert.equal(es1.url, '/api/events');

      es1.simulateOpen();
      assert.equal(connectionStates[0].status, 'connected');

      // Deliver an event with ID
      es1.simulateEvent('widget.update', { widget_id: 'calendar' }, 'evt-cal-42');
      assert.equal(sse.getLastEventId(), 'evt-cal-42');

      // Trigger connection drop
      es1.simulateError(new Error('Network offline'));
      assert.equal(es1.readyState, 2); // EventSource closed
      assert.equal(connectionStates[1].status, 'disconnected');
      assert.equal(connectionStates[1].lastEventId, 'evt-cal-42');

      // Await reconnection backoff timer
      await new Promise((resolve) => setTimeout(resolve, 60));

      // Second EventSource instance should have been constructed with lastEventId
      assert.equal(MockEventSource.instances.length, 2);
      let es2 = MockEventSource.instances[1];
      assert.equal(es2.url, '/api/events?lastEventId=evt-cal-42');

      es2.simulateOpen();
      assert.equal(connectionStates[2].status, 'connected');
      assert.equal(connectionStates[2].lastEventId, 'evt-cal-42');

      sse.disconnect();
    });

    test('dynamically attaches event listeners registered after connect()', () => {
      MockEventSource.instances = [];
      const sse = new MirrormereSSE('/api/events', { EventSource: MockEventSource });

      sse.connect();
      const es = MockEventSource.instances[0];

      let customPayload = null;
      sse.on('custom.sensor', (data) => {
        customPayload = data;
      });

      es.simulateEvent('custom.sensor', { temp: 72.5 }, 'evt-sensor-99');
      assert.equal(sse.getLastEventId(), 'evt-sensor-99');
      assert.deepEqual(customPayload, { temp: 72.5 });

      sse.disconnect();
    });

    test('resets state completely on reset()', () => {
      const sse = new MirrormereSSE('/api/events', {
        EventSource: MockEventSource,
        lastEventId: 'initial-id',
      });
      assert.equal(sse.getLastEventId(), 'initial-id');
      assert.equal(sse.getConnectionURL(), '/api/events?lastEventId=initial-id');

      sse.reset();
      assert.equal(sse.getLastEventId(), null);
      assert.equal(sse.getConnectionURL(), '/api/events');
    });
  });
});
