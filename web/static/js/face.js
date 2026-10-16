/**
 * Mirrormere Amos State Word overlay (prototype).
 * Shows the current voice state as one uppercase word in the header bar, beside the status light.
 * Mounted only when the page URL carries ?face=1; otherwise nothing is added to the DOM.
 */
(() => {
  const WORDS = {
    idle: 'IDLE',
    listening: 'LISTENING',
    transcribing: 'TRANSCRIBING',
    thinking: 'THINKING',
    synthesizing: 'SYNTHESIZING',
    speaking: 'SPEAKING',
    error: 'ERROR',
  };

  const ERROR_RESET_MS = 4000;

  function hasFaceParam() {
    const search = typeof window !== 'undefined' && window.location ? window.location.search : '';
    return new URLSearchParams(search || '').get('face') === '1';
  }

  /**
   * FaceController renders the voice state word and resets error back to idle.
   */
  class FaceController {
    /**
     * @param {Object} [options={}]
     * @param {Object} [options.sseClient] SSE client exposing on(eventType, callback)
     * @param {Document} [options.document] defaults to the global document
     */
    constructor(options = {}) {
      this.sseClient = options.sseClient || null;
      this.document = options.document || (typeof document !== 'undefined' ? document : null);
      this.element = null;
      this.resetTimer = null;

      if (!hasFaceParam() || !this.document) return;

      const el = this.document.createElement('div');
      el.id = 'amos-face';
      el.className = 'amos-face';
      el.setAttribute('aria-live', 'polite');
      el.dataset.state = 'idle';
      el.textContent = WORDS.idle;
      // Header bar, right side, just before the system status light; when the
      // header is absent (a page without it) fall back to the body.
      const header = typeof this.document.querySelector === 'function'
        ? this.document.querySelector('.header-status')
        : null;
      if (header && typeof header.insertBefore === 'function') {
        header.insertBefore(el, header.firstChild || null);
      } else {
        this.document.body.appendChild(el);
      }
      this.element = el;

      if (this.sseClient && typeof this.sseClient.on === 'function') {
        this.sseClient.on('voice.state', (data) => {
          this.setState(data && data.state);
        });
      }
    }

    /**
     * Sets the displayed state. Any call cancels a pending error reset.
     * Unknown states are shown as idle.
     * @param {string} state
     */
    setState(state) {
      this.clearResetTimer();

      const key = Object.prototype.hasOwnProperty.call(WORDS, state) ? state : 'idle';
      if (this.element) {
        this.element.dataset.state = key;
        this.element.textContent = WORDS[key];
      }

      if (key === 'error') {
        this.resetTimer = setTimeout(() => {
          this.resetTimer = null;
          this.setState('idle');
        }, ERROR_RESET_MS);
      }
    }

    clearResetTimer() {
      if (this.resetTimer !== null) {
        clearTimeout(this.resetTimer);
        this.resetTimer = null;
      }
    }
  }

  if (typeof window !== 'undefined') {
    window.MirrormereFace = {
      FaceController,
      WORDS,
    };
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
      FaceController,
      WORDS,
    };
  }
})();
