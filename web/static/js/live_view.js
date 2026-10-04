/**
 * Mirrormere Live View Widget Controller
 * Manages ambient video tile streams (WebRTC, MJPEG, HLS), one-tap expand-to-play CQRS handoff,
 * IntersectionObserver off-screen decoder teardown, and SSE synchronization.
 * Complies with SPEC-003, SPEC-004, and SPEC-015.
 */
(() => {
  class LiveViewInstance {
    constructor(element, options = {}) {
      this.element = element;
      this.options = options;
      this.widgetId = element.dataset.widgetId || 'live-view';
      this.config = this.parseConfig();

      this.videoEl = element.querySelector('video.live-view-media');
      this.imgEl = element.querySelector('img.live-view-mjpeg');
      this.posterEl = element.querySelector('img.live-view-poster');

      this.pc = null;
      this.observer = null;
      this.isLoading = false;
      this.isIntersecting = true;
      this.mountEpoch = 0;
      this.safetyTimer = null;

      this.fetchFn = options.fetch || (typeof fetch !== 'undefined' ? fetch.bind(window) : null);
      this.PeerConnectionClass = options.RTCPeerConnection || (typeof RTCPeerConnection !== 'undefined' ? RTCPeerConnection : null);
      this.sseClient = options.sseClient || (typeof window !== 'undefined' ? window.sseClient : null);

      this.init();
    }

    parseConfig() {
      const configEl = this.element.querySelector('.live-view-config');
      if (configEl && configEl.textContent) {
        try {
          let parsed = JSON.parse(configEl.textContent.trim()) || {};
          if (typeof parsed === 'string') {
            try {
              parsed = JSON.parse(parsed) || {};
            } catch (_) {}
          }
          return parsed;
        } catch (e) {
          console.warn(`[MirrormereLiveView] Failed to parse config for ${this.widgetId}:`, e);
        }
      }
      return {};
    }

    init() {
      // 1. Click-to-Expand gesture with in-flight lock
      this.bindClickGesture();

      // 2. IntersectionObserver for carousel off-screen decoder hygiene
      this.bindIntersectionObserver();

      // 3. SSE synchronization for fullscreen mode handoff
      this.bindSSE();

      // 4. Initial stream activation if in viewport
      if (this.isIntersecting) {
        this.startStream();
      }
    }

    bindClickGesture() {
      this.clickHandler = async (e) => {
        if (this.config.expand_on_click === false) return;
        if (this.isLoading) return;

        this.isLoading = true;
        this.element.classList.add('loading');

        // Safety timeout to release in-flight lock if network or SSE stalls
        if (this.safetyTimer) clearTimeout(this.safetyTimer);
        this.safetyTimer = setTimeout(() => {
          this.isLoading = false;
          this.element.classList.remove('loading');
        }, 2000);

        const payload = {
          id: this.config.stream_id || this.widgetId,
          stream_url: this.config.expand_stream_url || this.config.stream_url,
          type: this.config.expand_stream_type || this.config.stream_type || 'webrtc',
          priority: this.config.priority || 'persistent',
          controllable: Boolean(this.config.controllable),
          control_url: this.config.control_url || '',
        };

        if (this.config.timeout_seconds && Number(this.config.timeout_seconds) > 0) {
          payload.timeout_seconds = Number(this.config.timeout_seconds);
        }

        try {
          if (this.fetchFn) {
            await this.fetchFn('api/video/trigger', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(payload),
            });
          }
        } catch (err) {
          console.warn(`[MirrormereLiveView] Trigger failed for ${this.widgetId}:`, err);
          this.isLoading = false;
          this.element.classList.remove('loading');
        }
      };

      this.element.addEventListener('click', this.clickHandler);
    }

    bindIntersectionObserver() {
      const ObserverClass = this.options.IntersectionObserver || (typeof IntersectionObserver !== 'undefined' ? IntersectionObserver : null);
      if (!ObserverClass) return;

      this.observer = new ObserverClass((entries) => {
        for (const entry of entries) {
          if (entry.target === this.element) {
            const isVisible = Boolean(entry.isIntersecting);
            if (isVisible !== this.isIntersecting) {
              this.isIntersecting = isVisible;
              if (isVisible) {
                this.startStream();
              } else {
                this.pauseStream();
              }
            }
          }
        }
      }, { threshold: 0.1 });

      this.observer.observe(this.element);
    }

    bindSSE() {
      if (!this.sseClient) return;

      this.videoStateHandler = (data) => {
        if (!data) return;
        const mode = data.mode || (data.primary ? 'video' : 'widgets');
        if (mode === 'video') {
          // Display switched to fullscreen video presentation: flush tile decoders
          this.pauseStream();
          this.isLoading = false;
          this.element.classList.remove('loading');
          if (this.safetyTimer) clearTimeout(this.safetyTimer);
        } else if (mode === 'widgets') {
          // Display returned to dashboard grid: resume tile if visible
          if (this.isIntersecting) {
            this.startStream();
          }
        }
      };

      if (typeof this.sseClient.on === 'function') {
        this.sseClient.on('video.state', this.videoStateHandler);
      } else if (typeof this.sseClient.addEventListener === 'function') {
        this.sseClient.addEventListener('video.state', (evt) => {
          try {
            const parsed = typeof evt.data === 'string' ? JSON.parse(evt.data) : evt.data;
            this.videoStateHandler(parsed);
          } catch (_) {}
        });
      }
    }

    async startStream() {
      if (this.pc) {
        this.pauseStream();
      }

      this.mountEpoch++;
      const currentEpoch = this.mountEpoch;
      const streamType = (this.config.stream_type || 'webrtc').toLowerCase();

      if (streamType === 'mjpeg') {
        if (this.imgEl && this.config.stream_url) {
          this.imgEl.src = this.config.stream_url;
        }
        return;
      }

      // Default WebRTC playback
      if (!this.videoEl || !this.config.stream_url) return;
      this.videoEl.muted = true;
      this.videoEl.volume = 0;

      if (!this.PeerConnectionClass) return;

      try {
        const pc = new this.PeerConnectionClass();
        this.pc = pc;

        if (typeof pc.addTransceiver === 'function') {
          pc.addTransceiver('video', { direction: 'recvonly' });
        }

        pc.ontrack = (event) => {
          if (currentEpoch !== this.mountEpoch) return;
          if (event.streams && event.streams[0]) {
            this.videoEl.srcObject = event.streams[0];
          } else {
            const MediaStreamCtor = typeof MediaStream !== 'undefined' ? MediaStream : (typeof window !== 'undefined' ? window.MediaStream : null);
            if (!this.videoEl.srcObject && MediaStreamCtor) {
              this.videoEl.srcObject = new MediaStreamCtor();
            }
            if (this.videoEl.srcObject && typeof this.videoEl.srcObject.addTrack === 'function' && event.track) {
              this.videoEl.srcObject.addTrack(event.track);
            }
          }
        };

        const offer = await pc.createOffer();
        if (currentEpoch !== this.mountEpoch) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        await pc.setLocalDescription(offer);
        if (currentEpoch !== this.mountEpoch) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        // Wait for ICE gathering completion or timeout
        await new Promise((resolve) => {
          if (pc.iceGatheringState === 'complete') return resolve();
          const checkState = () => {
            if (pc.iceGatheringState === 'complete') {
              pc.removeEventListener('icegatheringstatechange', checkState);
              resolve();
            }
          };
          if (typeof pc.addEventListener === 'function') {
            pc.addEventListener('icegatheringstatechange', checkState);
          }
          setTimeout(resolve, 500);
        });

        if (currentEpoch !== this.mountEpoch) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        if (this.fetchFn) {
          const sdpPayload = pc.localDescription ? (pc.localDescription.sdp || '') : '';
          const res = await this.fetchFn(this.config.stream_url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/sdp' },
            body: sdpPayload,
          });

          if (!res.ok) {
            throw new Error(`SDP exchange failed with HTTP ${res.status}`);
          }

          const responseText = await res.text();
          if (currentEpoch !== this.mountEpoch) {
            if (typeof pc.close === 'function') pc.close();
            return;
          }

          let remoteSdp = responseText;
          try {
            const jsonResp = JSON.parse(responseText);
            if (jsonResp && jsonResp.sdp) {
              remoteSdp = jsonResp.sdp;
            }
          } catch (_) {}

          await pc.setRemoteDescription({ type: 'answer', sdp: remoteSdp });
          if (typeof this.videoEl.play === 'function') {
            try {
              const playPromise = this.videoEl.play();
              if (playPromise && typeof playPromise.catch === 'function') {
                playPromise.catch(() => {});
              }
            } catch (_) {}
          }
        }
      } catch (err) {
        console.warn(`[MirrormereLiveView] WebRTC negotiation error for ${this.widgetId}:`, err);
      }
    }

    pauseStream() {
      this.mountEpoch++;
      if (this.pc) {
        try {
          if (typeof this.pc.close === 'function') this.pc.close();
        } catch (_) {}
        this.pc = null;
      }
      if (this.videoEl) {
        this.videoEl.srcObject = null;
        try {
          if (typeof this.videoEl.pause === 'function') this.videoEl.pause();
        } catch (_) {}
      }
      if (this.imgEl) {
        this.imgEl.src = '';
      }
    }

    destroy() {
      this.pauseStream();
      if (this.safetyTimer) clearTimeout(this.safetyTimer);
      if (this.element && this.clickHandler) {
        this.element.removeEventListener('click', this.clickHandler);
      }
      if (this.observer) {
        this.observer.disconnect();
        this.observer = null;
      }
    }
  }

  window.MirrormereLiveView = {
    instances: new Map(),

    mount(element, options = {}) {
      if (!element) return null;
      const widgetId = element.dataset.widgetId || 'live-view';
      if (this.instances.has(widgetId)) {
        this.unmount(widgetId);
      }
      const instance = new LiveViewInstance(element, options);
      this.instances.set(widgetId, instance);
      return instance;
    },

    unmount(widgetId) {
      if (this.instances.has(widgetId)) {
        const inst = this.instances.get(widgetId);
        inst.destroy();
        this.instances.delete(widgetId);
      }
    },

    getInstance(widgetId) {
      return this.instances.get(widgetId) || null;
    }
  };
})();
