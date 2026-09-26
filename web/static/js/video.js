/**
 * Mirrormere Video Presentation Mode & Multi-Stream Player Controller
 * Manages client-side video mode switching ('widgets' <-> 'video'),
 * the multi-stream protocol triad (webrtc, hls, mjpeg), floating PiP dock,
 * interactive zero-reparenting swap-on-tap, and audio ceiling integration.
 * Complies with SPEC-004 §1–§5, SPEC-006 §4, and SPEC-010 §1.
 */
(() => {
  /**
   * VideoPlayerManager coordinates the video presentation stage and stream playback.
   */
  class VideoPlayerManager {
    /**
     * @param {Object} [options={}]
     * @param {HTMLElement} [options.stageElement] - The #video-stage root container.
     * @param {HTMLElement} [options.primarySlot] - The #video-primary-slot container.
     * @param {HTMLElement} [options.pipSlot] - The #video-pip-slot container.
     * @param {Object} [options.audioManager] - Chunk 4.1 AudioManager instance.
     * @param {Object} [options.carousel] - MirrormereCarousel instance.
     * @param {Function} [options.RTCPeerConnection] - Custom/mock RTCPeerConnection constructor.
     * @param {Function} [options.fetch] - Custom fetch implementation (for hermetic testing).
     */
    constructor(options = {}) {
      this.options = options;
      this.stage = options.stageElement || (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);
      this.primarySlot = options.primarySlot || (typeof document !== 'undefined' ? document.getElementById('video-primary-slot') : null);
      this.pipSlot = options.pipSlot || (typeof document !== 'undefined' ? document.getElementById('video-pip-slot') : null);
      this.audioManager = options.audioManager || (typeof window !== 'undefined' ? window.audioManager : null);
      this.carousel = options.carousel || (typeof window !== 'undefined' ? window.carousel : null);
      this.hud = options.hud || null;
      this.fetchFn = options.fetch || (typeof fetch !== 'undefined' ? fetch : null);
      this.PeerConnectionClass = options.RTCPeerConnection || (typeof RTCPeerConnection !== 'undefined' ? RTCPeerConnection : null);

      this.currentMode = 'widgets';
      this.isSwapped = false;
      this.mountEpoch = 0;
      this.sessionTokenSeq = 0;

      // Track server-assigned state
      this.serverPrimary = null;
      this.serverPip = null;

      // Active stream playback sessions: { stream, element, pc, hls, type }
      this.primarySession = null;
      this.pipSession = null;

      this.bindSlotGestures();
    }

    /**
     * Bind touch/click gestures on PiP and primary slots for interactive swap-on-tap.
     */
    bindSlotGestures() {
      if (this.pipSlot) {
        this.pipSlot.addEventListener('click', (e) => {
          if (!this.isSwapped) {
            e.stopPropagation();
            this.handleSlotClick('pip');
          }
        });
      }

      if (this.primarySlot) {
        this.primarySlot.addEventListener('click', (e) => {
          // If in swapped state, the primarySlot is rendered visually as the corner PiP dock
          if (this.isSwapped) {
            e.stopPropagation();
            this.handleSlotClick('primary');
          }
        });
      }
    }

    /**
     * Handle slot tap interaction for zero-reparenting stream swapping.
     * @param {'primary'|'pip'} clickedSlot
     */
    handleSlotClick(clickedSlot) {
      if (!this.serverPip || !this.pipSession || !this.primarySession) {
        return; // Nothing to swap if PiP is not active
      }

      if (!this.isSwapped && clickedSlot === 'pip') {
        this.swapStreams();
      } else if (this.isSwapped && clickedSlot === 'primary') {
        this.swapStreams();
      }
    }

    /**
     * Connect or update HUD controller reference.
     * @param {Object} hud - VideoHUDController instance.
     */
    setHUD(hud) {
      this.hud = hud;
    }

    /**
     * Swaps the active presentation and audio focus between primary and PiP streams
     * without reparenting DOM nodes or dropping WebRTC/MSE playback.
     */
    swapStreams() {
      if (!this.primarySession || !this.pipSession) {
        return;
      }

      this.isSwapped = !this.isSwapped;

      if (this.stage) {
        this.stage.dataset.swapped = this.isSwapped ? 'true' : 'false';
      }

      this.syncAudioFocus();

      if (this.hud && typeof this.hud.syncStream === 'function') {
        this.hud.syncStream();
      }
    }

    /**
     * Synchronize audio focus strictly:
     * - The presentation Primary stream is unmuted and registered with AudioManager.
     * - The presentation PiP stream is strictly muted, volume 0, and unregistered from AudioManager.
     */
    syncAudioFocus() {
      const activePrimarySession = this.isSwapped ? this.pipSession : this.primarySession;
      const activePipSession = this.isSwapped ? this.primarySession : this.pipSession;

      // 1. Configure active PiP stream (strictly muted)
      if (activePipSession && activePipSession.element) {
        if (activePipSession.type !== 'mjpeg') {
          if (this.audioManager && typeof this.audioManager.unregisterMediaElement === 'function') {
            this.audioManager.unregisterMediaElement(activePipSession.element);
          }
          activePipSession.element.muted = true;
          activePipSession.element.volume = 0;
        }
      }

      // 2. Configure active Primary stream (unmuted & audioManager registered)
      if (activePrimarySession && activePrimarySession.element) {
        if (activePrimarySession.type !== 'mjpeg') {
          activePrimarySession.element.muted = false;
          if (this.audioManager && typeof this.audioManager.registerMediaElement === 'function') {
            this.audioManager.registerMediaElement(activePrimarySession.element);
          }
        }
      }
    }

    /**
     * Process incoming video.state SSE events.
     * @param {Object} data - Schema matching api/schemas/video.state.json
     */
    handleVideoState(data) {
      if (!data) return;

      this.mountEpoch++;
      const mode = data.mode || (data.primary ? 'video' : 'widgets');

      if (mode === 'video' && data.primary) {
        this.enterVideoMode(data);
      } else {
        this.exitVideoMode();
      }
    }

    /**
     * Transition into video presentation mode and mount/reconcile active streams.
     * @param {Object} data
     */
    enterVideoMode(data) {
      this.currentMode = 'video';

      // 1. Pause carousel background processing
      if (this.carousel && typeof this.carousel.pause === 'function') {
        this.carousel.pause();
      }

      // 2. Hide dashboard grid canvas
      const canvasEl = typeof document !== 'undefined' ? document.getElementById('grid-canvas') : null;
      if (canvasEl) {
        canvasEl.style.display = 'none';
      }

      // 3. Reveal video stage
      if (this.stage) {
        this.stage.style.display = 'flex';
        this.stage.classList.add('active');
      }

      // 4. Reconcile PiP slot
      if (!data.pip) {
        // PiP stream was dismissed or absent
        if (this.isSwapped) {
          this.isSwapped = false;
          if (this.stage) this.stage.dataset.swapped = 'false';
        }
        if (this.pipSession) {
          this.teardownSession(this.pipSession);
          this.pipSession = null;
        }
        this.serverPip = null;
        if (this.pipSlot) {
          this.pipSlot.style.display = 'none';
        }
      } else {
        if (this.pipSlot) {
          this.pipSlot.style.display = 'flex';
        }
      }

      // 5. Reconcile Primary stream mount
      if (data.primary) {
        if (!this.primarySession || this.serverPrimary?.id !== data.primary.id || this.serverPrimary?.stream_url !== data.primary.stream_url) {
          if (this.primarySession) {
            this.teardownSession(this.primarySession);
            this.primarySession = null;
          }
          if (this.primarySlot) {
            this.primarySession = this.mountStream(data.primary, this.primarySlot, false);
          }
        } else {
          // Update properties if changed
          this.primarySession.stream = { ...this.primarySession.stream, ...data.primary };
        }
        this.serverPrimary = { ...data.primary };
      }

      // 6. Reconcile PiP stream mount
      if (data.pip) {
        if (!this.pipSession || this.serverPip?.id !== data.pip.id || this.serverPip?.stream_url !== data.pip.stream_url) {
          if (this.pipSession) {
            this.teardownSession(this.pipSession);
            this.pipSession = null;
          }
          if (this.pipSlot) {
            this.pipSession = this.mountStream(data.pip, this.pipSlot, true);
          }
        } else {
          this.pipSession.stream = { ...this.pipSession.stream, ...data.pip };
        }
        this.serverPip = { ...data.pip };
      }

      // 7. Enforce audio focus
      this.syncAudioFocus();
    }

    /**
     * Transition back to widgets mode and clean up all video sessions.
     */
    exitVideoMode() {
      this.currentMode = 'widgets';
      this.isSwapped = false;

      if (this.hud && typeof this.hud.hideHUD === 'function') {
        this.hud.hideHUD();
      }

      if (this.stage) {
        this.stage.dataset.swapped = 'false';
        this.stage.classList.remove('active');
        this.stage.style.display = 'none';
      }

      if (this.pipSlot) {
        this.pipSlot.style.display = 'none';
      }

      // 1. Cleanly teardown media sessions
      if (this.primarySession) {
        this.teardownSession(this.primarySession);
        this.primarySession = null;
      }
      if (this.pipSession) {
        this.teardownSession(this.pipSession);
        this.pipSession = null;
      }

      this.serverPrimary = null;
      this.serverPip = null;

      // 2. Restore dashboard grid canvas
      const canvasEl = typeof document !== 'undefined' ? document.getElementById('grid-canvas') : null;
      if (canvasEl) {
        canvasEl.style.display = '';
      }

      // 3. Resume carousel processing
      if (this.carousel && typeof this.carousel.resume === 'function') {
        this.carousel.resume();
      }
    }

    /**
     * Mounts a stream into the target slot container according to its protocol triad type.
     * @param {Object} stream - { id, stream_url, type, ... }
     * @param {HTMLElement} container - Target slot element.
     * @param {boolean} isPip - Whether the stream is initially mounted in PiP.
     * @returns {Object} Active session record.
     */
    mountStream(stream, container, isPip) {
      if (!container) return null;
      container.innerHTML = '';

      const type = (stream.type || 'webrtc').toLowerCase();

      if (type === 'mjpeg') {
        return this.mountMJPEG(stream, container);
      }

      if (type === 'hls') {
        return this.mountHLS(stream, container, isPip);
      }

      // Default to WebRTC
      return this.mountWebRTC(stream, container, isPip);
    }

    /**
     * Mounts MJPEG stream via responsive <img> element.
     */
    mountMJPEG(stream, container) {
      const img = document.createElement('img');
      img.className = 'video-stream-media';
      img.alt = `Stream ${stream.id}`;
      img.src = stream.stream_url;

      container.appendChild(img);
      return { stream, element: img, type: 'mjpeg', token: ++this.sessionTokenSeq, cancelled: false };
    }

    /**
     * Mounts HLS stream via native video or hls.js fallback.
     */
    mountHLS(stream, container, isPip) {
      const video = document.createElement('video');
      video.className = 'video-stream-media';
      video.autoplay = true;
      video.playsInline = true;
      video.muted = isPip;
      if (isPip) video.volume = 0;

      let hlsInstance = null;
      const Hls = (typeof window !== 'undefined' && typeof window.Hls !== 'undefined') ? window.Hls : null;

      if (Hls && typeof Hls.isSupported === 'function' && Hls.isSupported()) {
        hlsInstance = new Hls();
        hlsInstance.loadSource(stream.stream_url);
        hlsInstance.attachMedia(video);
        hlsInstance.on(Hls.Events.ERROR, (event, data) => {
          if (data && data.fatal) {
            console.warn('[MirrormereVideo] Fatal HLS error encountered:', data);
            if (typeof hlsInstance.destroy === 'function') hlsInstance.destroy();
          }
        });
      } else if (typeof video.canPlayType === 'function' && video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = stream.stream_url;
      } else {
        console.warn('[MirrormereVideo] HLS playback not natively supported and hls.js is absent');
        video.src = stream.stream_url;
      }

      container.appendChild(video);
      this.safePlay(video);

      return { stream, element: video, hls: hlsInstance, type: 'hls', token: ++this.sessionTokenSeq, cancelled: false };
    }

    /**
     * Mounts WebRTC stream using RTCPeerConnection and SDP offer/answer exchange.
     */
    mountWebRTC(stream, container, isPip) {
      const video = document.createElement('video');
      video.className = 'video-stream-media';
      video.autoplay = true;
      video.playsInline = true;
      video.muted = isPip;
      if (isPip) video.volume = 0;

      container.appendChild(video);

      const session = {
        stream,
        element: video,
        pc: null,
        type: 'webrtc',
        token: ++this.sessionTokenSeq,
        cancelled: false,
      };

      if (this.PeerConnectionClass) {
        try {
          const pc = new this.PeerConnectionClass();
          session.pc = pc;

          if (typeof pc.addTransceiver === 'function') {
            pc.addTransceiver('video', { direction: 'recvonly' });
            pc.addTransceiver('audio', { direction: 'recvonly' });
          }

          pc.ontrack = (event) => {
            if (session.cancelled) return;
            if (event.streams && event.streams[0]) {
              video.srcObject = event.streams[0];
            } else {
              const MediaStreamConstructor = typeof MediaStream !== 'undefined' ? MediaStream : (typeof window !== 'undefined' ? window.MediaStream : null);
              if (!video.srcObject && MediaStreamConstructor) {
                video.srcObject = new MediaStreamConstructor();
              }
              if (video.srcObject && typeof video.srcObject.addTrack === 'function' && event.track) {
                video.srcObject.addTrack(event.track);
              }
            }
          };

          this.negotiateWebRTC(session, stream.stream_url, video);
        } catch (err) {
          console.warn('[MirrormereVideo] RTCPeerConnection initialization error:', err);
        }
      }

      this.safePlay(video);
      return session;
    }

    /**
     * Performs async WebRTC offer/answer SDP negotiation with go2rtc endpoint.
     */
    async negotiateWebRTC(session, streamUrl, video) {
      const pc = session ? session.pc : null;
      if (!pc) return;

      try {
        const offer = await pc.createOffer();
        if (session.cancelled) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        await pc.setLocalDescription(offer);
        if (session.cancelled) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        // Wait for ICE gathering completion or 500ms safety timeout
        await new Promise((resolve) => {
          if (pc.iceGatheringState === 'complete') return resolve();
          const check = () => {
            if (pc.iceGatheringState === 'complete') {
              if (typeof pc.removeEventListener === 'function') {
                pc.removeEventListener('icegatheringstatechange', check);
              }
              resolve();
            }
          };
          if (typeof pc.addEventListener === 'function') {
            pc.addEventListener('icegatheringstatechange', check);
          }
          setTimeout(resolve, 500);
        });

        if (session.cancelled) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        const sdpPayload = pc.localDescription ? pc.localDescription.sdp : offer.sdp;
        const fetchFn = this.fetchFn || fetch;

        const res = await fetchFn(streamUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/sdp' },
          body: sdpPayload,
        });

        if (session.cancelled) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }

        const text = await res.text();
        if (session.cancelled) {
          if (typeof pc.close === 'function') pc.close();
          return;
        }

        let answerSDP = text;
        try {
          const json = JSON.parse(text);
          if (json && json.sdp) {
            answerSDP = json.sdp;
          }
        } catch {
          // Payload is raw SDP text
        }

        await pc.setRemoteDescription({ type: 'answer', sdp: answerSDP });
      } catch (err) {
        if (!session.cancelled) {
          console.warn('[MirrormereVideo] WebRTC SDP negotiation failed:', err);
        }
        if (typeof pc.close === 'function') pc.close();
      }
    }

    /**
     * Safely initiates video playback with autoplay rejection protection.
     */
    safePlay(video) {
      if (!video || typeof video.play !== 'function') return;
      try {
        const p = video.play();
        if (p !== undefined && typeof p.catch === 'function') {
          p.catch((err) => {
            console.warn('[MirrormereVideo] Playback prevented by browser autoplay policy:', err);
          });
        }
      } catch (e) {
        // Ignored
      }
    }

    /**
     * Hermetic teardown of a media session freeing decoders, sinks, and peer connections.
     */
    teardownSession(session) {
      if (!session) return;
      session.cancelled = true;

      if (this.audioManager && session.element && typeof this.audioManager.unregisterMediaElement === 'function') {
        this.audioManager.unregisterMediaElement(session.element);
      }

      if (session.element) {
        if (session.type === 'mjpeg') {
          session.element.src = '';
          session.element.removeAttribute('src');
          session.element.onload = null;
          session.element.onerror = null;
        } else {
          if (typeof session.element.pause === 'function') session.element.pause();
          session.element.srcObject = null;
          session.element.removeAttribute('src');
          if (typeof session.element.load === 'function') session.element.load();
        }

        if (session.element.parentNode) {
          session.element.parentNode.removeChild(session.element);
        }
      }

      if (session.pc) {
        session.pc.ontrack = null;
        session.pc.onicecandidate = null;
        if (typeof session.pc.close === 'function') {
          session.pc.close();
        }
      }

      if (session.hls && typeof session.hls.destroy === 'function') {
        session.hls.destroy();
      }
    }

    /**
     * Inspect active runtime state for testing and HUD rendering.
     */
    getState() {
      return {
        mode: this.currentMode,
        isSwapped: this.isSwapped,
        primary: this.serverPrimary,
        pip: this.serverPip,
        hasPrimarySession: Boolean(this.primarySession),
        hasPipSession: Boolean(this.pipSession),
      };
    }

    /**
     * Complete teardown of manager instance.
     */
    destroy() {
      this.exitVideoMode();
    }
  }

  const api = {
    VideoPlayerManager,
  };

  if (typeof window !== 'undefined') {
    window.MirrormereVideo = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
})();
