/**
 * Mirrormere Voice Waveform Mouth.
 * Draws a 33-point waveform in the bottom-right corner of the display, driven by the real reply
 * audio levels that the ear daemon streams on GET /levels (SSE, "frame" events of 33 values in -100..100).
 * The mouth is shown only while the last voice state is "speaking" and a valid frame arrived
 * within LEVEL_STALE_MS. Nothing is mounted or connected unless a levels URL is configured.
 */
(() => {
  const SVG_NS = 'http://www.w3.org/2000/svg';
  const MOUTH_POINTS = 33;
  const MOUTH_X0 = 20;
  const MOUTH_STEP = 1.875;
  const LEVEL_GAIN = 2.2;
  const LEVEL_SPAN = 22;
  const LEVEL_STALE_MS = 400;
  const EASE_TAU_MS = 20;
  const EASE_MAX_DT_MS = 100;
  const DECAY_TAU_MS = 120;
  const DRAW_MIN_MS = 25;

  /**
   * Spatial smoothing for a 33-value level frame: one pass of the kernel [1, 2, 1] / 4.
   * Edge values reuse themselves as the missing neighbour. Returns a new array; input is not mutated.
   * @param {number[]} values
   * @returns {number[]}
   */
  function smoothValues(values) {
    const last = values.length - 1;
    const out = new Array(values.length);
    for (let i = 0; i <= last; i += 1) {
      const left = values[i > 0 ? i - 1 : 0];
      const right = values[i < last ? i + 1 : last];
      out[i] = (left + 2 * values[i] + right) / 4;
    }
    return out;
  }

  /**
   * Maps 33 waveform levels (-100..100) to mouth points.
   * Values are smoothed first; gain scales the level before clamping to the mouth span.
   * @param {number[]} values
   * @param {number} [gain=2.2]
   * @returns {Array<[number, number]>}
   */
  function framePoints(values, gain = LEVEL_GAIN) {
    const smoothed = smoothValues(values);
    const points = [];
    for (let i = 0; i < MOUTH_POINTS; i += 1) {
      const level = clamp((smoothed[i] / 100) * gain, -1, 1);
      points.push([MOUTH_X0 + i * MOUTH_STEP, 50 - level * LEVEL_SPAN]);
    }
    return points;
  }

  /**
   * Exponential approach from current toward target, one step per frame.
   * dtMs is clamped to 0..100. A null current, or one of a different length, returns a copy of target.
   * @param {number[]|null} current
   * @param {number[]} target
   * @param {number} dtMs
   * @param {number} [tauMs=20]
   * @returns {number[]}
   */
  function easeToward(current, target, dtMs, tauMs = EASE_TAU_MS) {
    if (!Array.isArray(current) || current.length !== target.length) return target.slice();
    const dt = Number.isFinite(dtMs) ? clamp(dtMs, 0, EASE_MAX_DT_MS) : 0;
    const k = 1 - Math.exp(-dt / tauMs);
    return target.map((t, i) => current[i] + (t - current[i]) * k);
  }

  function clamp(v, lo, hi) {
    return Math.min(hi, Math.max(lo, v));
  }

  function isLevelFrame(values) {
    return Array.isArray(values)
      && values.length === MOUTH_POINTS
      && values.every((v) => typeof v === 'number' && Number.isFinite(v));
  }

  function defaultClock() {
    return typeof performance !== 'undefined' && typeof performance.now === 'function'
      ? performance.now()
      : Date.now();
  }

  function defaultEventSourceFactory() {
    return typeof EventSource === 'function' ? (url) => new EventSource(url) : null;
  }

  function defaultReducedMotion() {
    try {
      return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
        ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
        : false;
    } catch (e) {
      return false;
    }
  }

  function formatPoints(points) {
    return points.map(([x, y]) => `${x.toFixed(2)},${y.toFixed(2)}`).join(' ');
  }

  /**
   * WaveformMouth draws the speaking waveform from the ear daemon's level stream.
   */
  class WaveformMouth {
    /**
     * @param {Object} [options={}]
     * @param {Object} [options.sseClient] SSE client exposing on(eventType, callback)
     * @param {Document} [options.document] defaults to the global document
     * @param {string} [options.levelsUrl] waveform SSE endpoint; falsy disables the mouth entirely
     * @param {number} [options.gain=2.2] amplification applied to level values
     * @param {Function|null} [options.eventSourceFactory] url => EventSource-like; null disables the stream
     * @param {Function} [options.clock] milliseconds clock; defaults to performance.now
     * @param {Function} [options.raf] requestAnimationFrame; defaults to window's
     * @param {Function} [options.caf] cancelAnimationFrame; defaults to window's
     * @param {boolean} [options.reducedMotion] draw the latest frame without easing; defaults to the media query
     */
    constructor(options = {}) {
      this.sseClient = options.sseClient || null;
      this.document = options.document || (typeof document !== 'undefined' ? document : null);
      this.levelsUrl = options.levelsUrl || '';
      this.gain = typeof options.gain === 'number' && options.gain > 0 ? options.gain : LEVEL_GAIN;
      this.eventSourceFactory = options.eventSourceFactory !== undefined
        ? options.eventSourceFactory
        : defaultEventSourceFactory();
      this.clock = options.clock || defaultClock;
      this.raf = options.raf || (typeof window !== 'undefined' && window.requestAnimationFrame
        ? window.requestAnimationFrame.bind(window) : null);
      this.caf = options.caf || (typeof window !== 'undefined' && window.cancelAnimationFrame
        ? window.cancelAnimationFrame.bind(window) : null);
      this.reducedMotion = options.reducedMotion !== undefined
        ? !!options.reducedMotion
        : defaultReducedMotion();

      this.element = null;
      this.polyline = null;
      this.voiceState = 'idle';
      this.levelFrame = null;
      this.levelAt = null;
      this.levelSource = null;
      this.shownY = null; // eased mouth y values, one per point
      this.looping = false;
      this.rafId = null;
      this.lastTick = null;

      if (!this.levelsUrl || !this.document) return;

      this.mount();
      this.openLevels();

      if (this.sseClient && typeof this.sseClient.on === 'function') {
        this.sseClient.on('voice.state', (data) => {
          this.setVoiceState(data && data.state);
        });
      }
    }

    /** Builds the inline SVG and appends it to the body. Skipped without SVG support. */
    mount() {
      const doc = this.document;
      if (typeof doc.createElementNS !== 'function' || !doc.body) return;

      const svg = doc.createElementNS(SVG_NS, 'svg');
      svg.setAttribute('class', 'mm-voice-waveform');
      svg.setAttribute('viewBox', '0 0 100 100');
      svg.setAttribute('aria-hidden', 'true');

      const polyline = doc.createElementNS(SVG_NS, 'polyline');
      polyline.setAttribute('class', 'mm-voice-waveform__line');

      svg.appendChild(polyline);
      doc.body.appendChild(svg);

      this.element = svg;
      this.polyline = polyline;
      this.drawMouth(framePoints(new Array(MOUTH_POINTS).fill(0), this.gain));
    }

    /**
     * Opens the level stream once. EventSource reconnects by itself. No-op without a factory.
     */
    openLevels() {
      if (!this.eventSourceFactory) return;
      let source = null;
      try {
        source = this.eventSourceFactory(this.levelsUrl);
      } catch (e) {
        return;
      }
      if (!source || typeof source.addEventListener !== 'function') return;
      source.addEventListener('frame', (e) => this.onLevelFrame(e));
      this.levelSource = source;
    }

    /** Stores a valid 33-value frame; malformed payloads are ignored. */
    onLevelFrame(e) {
      let values = null;
      try {
        values = JSON.parse(e ? e.data : undefined);
      } catch (err) {
        return;
      }
      if (!isLevelFrame(values)) return;
      this.levelFrame = values;
      this.levelAt = this.clock();
      this.refresh();
      if (this.voiceState === 'speaking' && (this.reducedMotion || !this.looping)) {
        this.drawFrame(0);
      }
    }

    /** True when a level frame arrived within the last LEVEL_STALE_MS. */
    hasFreshLevels() {
      return this.levelAt !== null && this.clock() - this.levelAt <= LEVEL_STALE_MS;
    }

    /** Shows the mouth only while speaking with fresh levels. Returns the visibility. */
    refresh() {
      const active = this.voiceState === 'speaking' && this.hasFreshLevels();
      if (this.element && this.element.classList) {
        this.element.classList.toggle('is-active', active);
      }
      return active;
    }

    /** Sets the mouth points from a point list. */
    drawMouth(points) {
      if (this.polyline) this.polyline.setAttribute('points', formatPoints(points));
    }

    /**
     * Draws one step. With fresh levels the mouth eases toward the latest frame (or jumps to it
     * under reduced motion); otherwise it eases flat.
     * @param {number} dt milliseconds since the previous step
     */
    drawFrame(dt) {
      if (this.voiceState === 'speaking' && this.hasFreshLevels()) {
        const targetY = framePoints(this.levelFrame, this.gain).map(([, y]) => y);
        this.shownY = this.reducedMotion ? targetY : easeToward(this.shownY, targetY, dt);
      } else {
        const flat = new Array(MOUTH_POINTS).fill(50);
        this.shownY = easeToward(this.shownY, flat, dt, DECAY_TAU_MS);
      }
      this.drawMouth(this.shownY.map((y, i) => [MOUTH_X0 + i * MOUTH_STEP, y]));
    }

    /** Starts the speaking loop once. No-op without a raf. */
    startLoop() {
      if (!this.raf || this.looping) return;
      this.looping = true;
      this.lastTick = null;
      const tick = (now) => {
        if (!this.looping) return;
        this.rafId = this.raf(tick);
        this.refresh();
        if (this.lastTick !== null && now - this.lastTick < DRAW_MIN_MS) return;
        const dt = this.lastTick === null ? 0 : now - this.lastTick;
        this.lastTick = now;
        this.drawFrame(dt);
      };
      this.rafId = this.raf(tick);
    }

    stopLoop() {
      if (this.looping && this.caf) this.caf(this.rafId);
      this.looping = false;
      this.rafId = null;
      this.lastTick = null;
    }

    /**
     * Records the voice state from a voice.state event. Leaving "speaking" hides the mouth
     * and drops stale levels so the next turn starts clean.
     * @param {*} state
     */
    setVoiceState(state) {
      this.voiceState = typeof state === 'string' ? state : 'idle';
      if (this.voiceState === 'speaking') {
        this.startLoop();
      } else {
        this.stopLoop();
        this.levelAt = null;
        this.shownY = null;
        this.drawMouth(framePoints(new Array(MOUTH_POINTS).fill(0), this.gain));
      }
      this.refresh();
    }
  }

  if (typeof window !== 'undefined') {
    window.MirrormereWaveform = {
      WaveformMouth,
      smoothValues,
      framePoints,
      easeToward,
    };
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
      WaveformMouth,
      smoothValues,
      framePoints,
      easeToward,
    };
  }
})();
