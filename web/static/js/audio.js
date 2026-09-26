/**
 * Mirrormere Master Audio Manager & Software Volume Ceiling
 * Manages client-side digital volume attenuation, mute state synchronization,
 * and 80% maximum element volume ceiling per SPEC-004 §3, SPEC-006 §5, and SPEC-010 §4.
 */

/**
 * Pure software volume calculation formula enforcing 80% maximum ceiling:
 * effective = (volume / 100) * 0.80 * (isDucked ? 0.20 : 1.0) * (isMuted ? 0.0 : 1.0)
 *
 * @param {number|string} volume - Volume percentage between 0 and 100.
 * @param {boolean} [isMuted=false] - Whether master mute is active.
 * @param {boolean} [isDucked=false] - Whether ducking attenuation is active (held false until voice pipeline bringup).
 * @returns {number} Effective volume clamped to [0.0, 0.80] and rounded to 4 decimals.
 */
function computeEffectiveVolume(volume, isMuted = false, isDucked = false) {
  const num = Number(volume);
  const vol = Number.isNaN(num) ? 0 : Math.max(0, Math.min(100, num));
  const muteMultiplier = isMuted ? 0.0 : 1.0;
  const duckMultiplier = isDucked ? 0.20 : 1.0;

  const raw = (vol / 100.0) * 0.80 * duckMultiplier * muteMultiplier;
  const clamped = Math.max(0.0, Math.min(0.80, raw));
  return Math.round(clamped * 10000) / 10000;
}

/**
 * Client-side AudioManager coordinating media elements with SSE audio.state updates.
 */
class AudioManager {
  /**
   * @param {Object} [options={}]
   * @param {number} [options.initialVolume=75]
   * @param {boolean} [options.initialMuted=false]
   * @param {Object} [options.sseClient=null]
   * @param {Array<HTMLMediaElement>} [options.mediaElements=[]]
   */
  constructor(options = {}) {
    this.volume = options.initialVolume !== undefined ? Number(options.initialVolume) : 75;
    this.muted = Boolean(options.initialMuted);
    this.ducked = false; // Held false until Phase 6 voice assistant bringup (SPEC-011)
    this.mediaElements = new Set();

    if (Array.isArray(options.mediaElements)) {
      for (const el of options.mediaElements) {
        this.registerMediaElement(el);
      }
    }

    if (options.sseClient) {
      this.attachSSE(options.sseClient);
    }
  }

  /**
   * Subscribes to audio.state events via MirrormereSSE (.on) or standard EventSource (.addEventListener).
   * @param {Object} sseClient
   */
  attachSSE(sseClient) {
    if (!sseClient) return;

    const handleEvent = (data) => {
      if (!data) return;
      this.updateAudioState(data);
    };

    if (typeof sseClient.on === 'function') {
      sseClient.on('audio.state', handleEvent);
    } else if (typeof sseClient.addEventListener === 'function') {
      sseClient.addEventListener('audio.state', (evt) => {
        try {
          const data = typeof evt.data === 'string' ? JSON.parse(evt.data) : evt.data;
          handleEvent(data);
        } catch (e) {
          // ignore malformed event payload
        }
      });
    }
  }

  /**
   * Updates internal state from audio.state event payload and applies volume to registered elements.
   * @param {Object} state
   * @param {number} [state.volume]
   * @param {boolean} [state.muted]
   */
  updateAudioState(state) {
    if (!state) return;
    if (state.volume !== undefined) {
      const v = Number(state.volume);
      if (!Number.isNaN(v)) {
        this.volume = Math.max(0, Math.min(100, v));
      }
    }
    if (state.muted !== undefined) {
      this.muted = Boolean(state.muted);
    }
    this.applyVolumeToAll();
  }

  /**
   * Calculates the current effective volume based on volume, mute, and ducking.
   * @returns {number}
   */
  getEffectiveVolume() {
    return computeEffectiveVolume(this.volume, this.muted, this.ducked);
  }

  /**
   * Registers an HTMLMediaElement (<video> or <audio>) and immediately applies current volume.
   * @param {HTMLMediaElement} element
   */
  registerMediaElement(element) {
    if (!element) return;
    this.mediaElements.add(element);
    try {
      element.volume = this.getEffectiveVolume();
    } catch (err) {
      // ignore
    }
  }

  /**
   * Unregisters an HTMLMediaElement.
   * @param {HTMLMediaElement} element
   */
  unregisterMediaElement(element) {
    if (!element) return;
    this.mediaElements.delete(element);
  }

  /**
   * Applies the current effective volume across all registered media elements,
   * safely pruning dead or disconnected elements from the set.
   */
  applyVolumeToAll() {
    const effective = this.getEffectiveVolume();
    for (const element of this.mediaElements) {
      if (!element || element.isConnected === false) {
        this.mediaElements.delete(element);
        continue;
      }
      try {
        element.volume = effective;
      } catch (err) {
        // ignore destroyed or detached elements
      }
    }
  }
}

// Global and CommonJS export bindings
if (typeof window !== 'undefined') {
  window.MirrormereAudio = {
    computeEffectiveVolume,
    AudioManager,
  };
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = {
    computeEffectiveVolume,
    AudioManager,
  };
}
