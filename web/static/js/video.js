/**
 * Mirrormere Video Presentation Mode & Multi-Stream Player Controller
 * Manages client-side video mode switching ('widgets' <-> 'video'),
 * the multi-stream protocol triad (webrtc, hls, mjpeg), floating PiP dock,
 * interactive zero-reparenting swap-on-tap, and audio ceiling integration.
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
      const win = options.window || (typeof window !== 'undefined' ? window : null);
      const defaultRemoteUrl = (win && win.MIRRORMERE_REMOTE_URL)
        || (typeof document !== 'undefined' && document.body && document.body.dataset && document.body.dataset.remoteUrl)
        || '';
      this.remoteUrl = options.remoteUrl !== undefined ? options.remoteUrl : defaultRemoteUrl;
      this.touchForwardingEnabled = options.touchForwardingEnabled !== undefined ? options.touchForwardingEnabled : true;
      const defaultFetch = (typeof window !== 'undefined' && typeof window.fetch === 'function')
        ? window.fetch.bind(window)
        : (typeof fetch === 'function' ? fetch : null);
      this.fetchFn = options.fetch || defaultFetch;
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

      this.paintFlushTimer = null;
      this.heartbeatElement = null;

      this.bindSlotGestures();
      this.ensureCompositorHeartbeat();
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
    exitVideoMode(flushPaint = true) {
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

      // 4. Explicit paint flush to release any pending Wayland wl_surface.frame callbacks
      if (flushPaint) {
        this.flushCompositorPaint();
      }
    }

    /**
     * Ensures the compositor keep-alive heartbeat DOM element exists.
     * Prevents Wayland/Cage output frame starvation on static views.
     */
    ensureCompositorHeartbeat() {
      if (typeof document === 'undefined') return null;
      let el = document.getElementById('mm-compositor-heartbeat');
      if (!el && document.body && typeof document.body.appendChild === 'function') {
        el = document.createElement('div');
        el.id = 'mm-compositor-heartbeat';
        el.className = 'mm-compositor-heartbeat';
        if (typeof el.setAttribute === 'function') {
          el.setAttribute('aria-hidden', 'true');
        }
        document.body.appendChild(el);
      }
      this.heartbeatElement = el;
      return el;
    }

    /**
     * Explicitly forces a layout reflow and multi-frame damage commit.
     * Guarantees that Chromium Ozone Wayland commits a buffer with damage
     * to satisfy wlroots/Cage frame callback expectations on stream teardown.
     */
    flushCompositorPaint() {
      if (typeof document === 'undefined') return;

      // 1. Force synchronous layout reflow pass
      if (document.body && typeof document.body.offsetHeight === 'number') {
        void document.body.offsetHeight;
      }

      const hb = this.heartbeatElement || this.ensureCompositorHeartbeat();

      // 2. Schedule multi-frame compositor damage burst
      const rAF = (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function')
        ? window.requestAnimationFrame.bind(window)
        : (typeof requestAnimationFrame === 'function' ? requestAnimationFrame : (cb) => setTimeout(cb, 16));

      const cancelRAF = (typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function')
        ? window.cancelAnimationFrame.bind(window)
        : (typeof cancelAnimationFrame === 'function' ? cancelAnimationFrame : clearTimeout);

      if (this.paintFlushTimer) {
        cancelRAF(this.paintFlushTimer);
        this.paintFlushTimer = null;
      }

      let frameCount = 0;
      const step = () => {
        frameCount++;
        if (hb) {
          if (hb.dataset) {
            hb.dataset.flush = String(frameCount);
          }
          if (hb.style) {
            hb.style.transform = (frameCount % 2 === 1) ? 'translateZ(0) scale(1.001)' : 'translateZ(0) scale(1)';
          }
        }
        if (document.body && typeof document.body.offsetHeight === 'number') {
          void document.body.offsetHeight;
        }

        if (frameCount < 2) {
          this.paintFlushTimer = rAF(step);
        } else {
          this.paintFlushTimer = null;
        }
      };

      this.paintFlushTimer = rAF(step);
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
      let session = null;

      if (type === 'mjpeg') {
        session = this.mountMJPEG(stream, container, isPip);
      } else if (type === 'hls') {
        session = this.mountHLS(stream, container, isPip);
      } else {
        // Default to WebRTC
        session = this.mountWebRTC(stream, container, isPip);
      }

      if (session) {
        this.bindTouchForwarding(session);
      }

      return session;
    }

    /**
     * Attaches cyberpunk stream loading spinner to primary video stage container.
     */
    attachStreamSpinner(session, container, mediaElement, isPip) {
      if (isPip || !container || typeof document === 'undefined') return;

      const spinner = document.createElement('div');
      spinner.className = 'video-stream-spinner';
      spinner.innerHTML = '<div class="video-spinner-ring"></div><span class="video-spinner-text">Waking Stream...</span>';
      container.appendChild(spinner);
      session.spinner = spinner;

      const dismissSpinner = () => {
        if (session.spinnerSafetyTimer) {
          clearTimeout(session.spinnerSafetyTimer);
          session.spinnerSafetyTimer = null;
        }
        if (!session.spinner || session.spinner.classList.contains('fade-out')) return;
        session.spinner.classList.add('fade-out');
        setTimeout(() => {
          if (session.spinner && session.spinner.parentNode) {
            session.spinner.parentNode.removeChild(session.spinner);
          }
        }, 400);
      };
      session.dismissSpinner = dismissSpinner;

      if (mediaElement && typeof mediaElement.addEventListener === 'function') {
        if (session.type === 'mjpeg') {
          mediaElement.addEventListener('load', dismissSpinner);
          mediaElement.addEventListener('error', dismissSpinner);
        } else {
          mediaElement.addEventListener('playing', dismissSpinner);
          mediaElement.addEventListener('loadeddata', dismissSpinner);
        }
      }

      session.spinnerSafetyTimer = setTimeout(dismissSpinner, 12000);
    }

    /**
     * Mounts MJPEG stream via responsive <img> element.
     */
    mountMJPEG(stream, container, isPip = false) {
      const img = document.createElement('img');
      img.className = 'video-stream-media';
      img.alt = `Stream ${stream.id}`;
      img.src = stream.stream_url;

      container.appendChild(img);
      const session = { stream, container, isPip: Boolean(isPip), element: img, type: 'mjpeg', token: ++this.sessionTokenSeq, cancelled: false };

      this.attachStreamSpinner(session, container, img, isPip);

      return session;
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
      video.style.touchAction = 'none';
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

      const session = { stream, container, isPip, element: video, hls: hlsInstance, type: 'hls', token: ++this.sessionTokenSeq, cancelled: false };

      this.attachStreamSpinner(session, container, video, isPip);

      return session;
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
      video.style.touchAction = 'none';
      if (isPip) video.volume = 0;

      container.appendChild(video);

      const session = {
        stream,
        container,
        isPip,
        element: video,
        pc: null,
        type: 'webrtc',
        token: ++this.sessionTokenSeq,
        cancelled: false,
        reconnectAttempts: 0,
        reconnectTimer: null,
        disconnectTimer: null,
      };

      this.attachStreamSpinner(session, container, video, isPip);

      if (this.PeerConnectionClass) {
        try {
          const pc = new this.PeerConnectionClass();
          session.pc = pc;

          if (typeof pc.addTransceiver === 'function') {
            const videoTransceiver = pc.addTransceiver('video', { direction: 'recvonly' });
            pc.addTransceiver('audio', { direction: 'recvonly' });
            if (videoTransceiver && videoTransceiver.receiver) {
              this.applyZeroLatencyPlayoutDelay(videoTransceiver.receiver, 'video');
            }
          }

          pc.ontrack = (event) => {
            if (session.cancelled) return;
            if (event.receiver && event.track && event.track.kind === 'video') {
              this.applyZeroLatencyPlayoutDelay(event.receiver, 'video');
            }
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

          const onConnChange = () => {
            if (session.pc) {
              const cs = session.pc.connectionState;
              const ics = session.pc.iceConnectionState;
              if (cs === 'failed' || cs === 'disconnected' || ics === 'failed' || ics === 'disconnected') {
                if (typeof session.dismissSpinner === 'function') {
                  session.dismissSpinner();
                }
              }
            }
            this.handleWebRTCConnectionChange(session);
          };
          if (typeof pc.addEventListener === 'function') {
            pc.addEventListener('iceconnectionstatechange', onConnChange);
            pc.addEventListener('connectionstatechange', onConnChange);
          }
          pc.oniceconnectionstatechange = onConnChange;
          pc.onconnectionstatechange = onConnChange;

          this.negotiateWebRTC(session, stream.stream_url, video);
        } catch (err) {
          console.warn('[MirrormereVideo] RTCPeerConnection initialization error:', err);
          if (!session.cancelled) {
            this.scheduleWebRTCReconnect(session);
          }
        }
      }

      this.safePlay(video);
      return session;
    }

    /**
     * Minimizes WebRTC receiver jitter buffer delay for ultra-low latency interactive streaming.
     * Restricts zero-delay hint strictly to video streams to prevent audio buffer starvation/crackling.
     * @param {RTCRtpReceiver} receiver
     * @param {'video'|'audio'} [kind='video']
     */
    applyZeroLatencyPlayoutDelay(receiver, kind = 'video') {
      if (!receiver || kind !== 'video') return;
      try {
        if ('playoutDelayHint' in receiver) {
          receiver.playoutDelayHint = 0;
        }
      } catch {
        // Non-fatal if browser blocks or throws on playoutDelayHint
      }
    }

    /**
     * Performs async WebRTC offer/answer SDP negotiation with go2rtc endpoint.
     */
    async negotiateWebRTC(session, streamUrl, video) {
      const pc = session ? session.pc : null;
      if (!pc) return;

      let timeoutId = null;
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

        const controller = (typeof AbortController !== 'undefined') ? new AbortController() : null;
        const timeoutMs = (typeof this.options.negotiationTimeout === 'number') ? this.options.negotiationTimeout : 5000;
        if (controller) {
          timeoutId = setTimeout(() => controller.abort(), timeoutMs);
        }

        const res = await fetchFn(streamUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/sdp' },
          body: sdpPayload,
          signal: controller ? controller.signal : undefined,
        });

        if (timeoutId) {
          clearTimeout(timeoutId);
          timeoutId = null;
        }

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
        if (typeof pc.getReceivers === 'function') {
          for (const receiver of pc.getReceivers()) {
            if (receiver && receiver.track && receiver.track.kind === 'video') {
              this.applyZeroLatencyPlayoutDelay(receiver, 'video');
            }
          }
        }
      } catch (err) {
        if (timeoutId) {
          clearTimeout(timeoutId);
          timeoutId = null;
        }
        if (!session.cancelled) {
          console.warn('[MirrormereVideo] WebRTC SDP negotiation failed:', err);
          if (typeof pc.close === 'function') pc.close();
          this.scheduleWebRTCReconnect(session);
        }
      }
    }

    /**
     * Watches RTCPeerConnection and ICE connection state changes for stream health.
     */
    handleWebRTCConnectionChange(session) {
      if (!session || session.cancelled || !session.pc) return;

      const connState = session.pc.connectionState;
      const iceState = session.pc.iceConnectionState;

      // 1. Healthy connected state: reset retry count and cancel active timers
      if (connState === 'connected' || iceState === 'connected' || iceState === 'completed') {
        if (session.disconnectTimer) {
          clearTimeout(session.disconnectTimer);
          session.disconnectTimer = null;
        }
        if (session.reconnectTimer) {
          clearTimeout(session.reconnectTimer);
          session.reconnectTimer = null;
        }
        session.reconnectAttempts = 0;
        return;
      }

      // 2. Active renegotiation in progress: clear disconnect timer to avoid racing
      if (connState === 'connecting' || iceState === 'checking') {
        if (session.disconnectTimer) {
          clearTimeout(session.disconnectTimer);
          session.disconnectTimer = null;
        }
        return;
      }

      // 3. Definite failure: trigger reconnect immediately
      if (connState === 'failed' || iceState === 'failed') {
        if (typeof session.dismissSpinner === 'function') {
          session.dismissSpinner();
        }
        if (session.disconnectTimer) {
          clearTimeout(session.disconnectTimer);
          session.disconnectTimer = null;
        }
        this.scheduleWebRTCReconnect(session);
        return;
      }

      // 4. Transient disconnect: arm grace period before tearing down
      if (connState === 'disconnected' || iceState === 'disconnected') {
        if (typeof session.dismissSpinner === 'function') {
          session.dismissSpinner();
        }
        if (!session.disconnectTimer) {
          const timeout = (typeof this.options.disconnectTimeout === 'number') ? this.options.disconnectTimeout : 2000;
          session.disconnectTimer = setTimeout(() => {
            session.disconnectTimer = null;
            if (session.cancelled) return;
            this.scheduleWebRTCReconnect(session);
          }, timeout);
        }
        return;
      }

      // 5. Unexpected close
      if ((connState === 'closed' || iceState === 'closed') && !session.cancelled) {
        this.scheduleWebRTCReconnect(session);
      }
    }

    /**
     * Schedules auto-reconnection with exponential backoff or triggers fallback if retries exhausted.
     */
    scheduleWebRTCReconnect(session) {
      if (!session || session.cancelled || session.reconnectTimer) return;

      const maxAttempts = (typeof this.options.maxReconnectAttempts === 'number') ? this.options.maxReconnectAttempts : 3;
      if (session.reconnectAttempts >= maxAttempts) {
        console.warn(`[MirrormereVideo] WebRTC stream ${session.stream?.id} reconnect attempts exhausted (${maxAttempts}). Falling back.`);
        this.handleStreamFallback(session);
        return;
      }

      session.reconnectAttempts++;
      const baseDelay = (typeof this.options.reconnectBaseDelay === 'number') ? this.options.reconnectBaseDelay : 1000;
      const factor = (typeof this.options.reconnectBackoffFactor === 'number') ? this.options.reconnectBackoffFactor : 1.5;
      const delay = Math.round(baseDelay * Math.pow(factor, session.reconnectAttempts - 1));

      session.reconnectTimer = setTimeout(async () => {
        session.reconnectTimer = null;
        if (session.cancelled) return;
        await this.reconnectWebRTC(session);
      }, delay);
    }

    /**
     * Closes dead peer connection, flushes hardware decode buffer, and establishes fresh WebRTC session.
     */
    async reconnectWebRTC(session) {
      if (!session || session.cancelled) return;

      // 1. Tear down old peer connection and unbind handlers to avoid leaks
      if (session.pc) {
        session.pc.oniceconnectionstatechange = null;
        session.pc.onconnectionstatechange = null;
        session.pc.ontrack = null;
        session.pc.onicecandidate = null;
        if (typeof session.pc.close === 'function') {
          session.pc.close();
        }
        session.pc = null;
      }

      // 2. Explicitly flush hardware decode frame buffer to prevent VAAPI/EGL kernel D-state lockup
      if (session.element) {
        session.element.srcObject = null;
        if (typeof session.element.load === 'function') {
          session.element.load();
        }
      }

      if (session.cancelled || !this.PeerConnectionClass) return;

      // 3. Create fresh RTCPeerConnection and re-bind event listeners
      try {
        const pc = new this.PeerConnectionClass();
        session.pc = pc;

        if (typeof pc.addTransceiver === 'function') {
          const videoTransceiver = pc.addTransceiver('video', { direction: 'recvonly' });
          pc.addTransceiver('audio', { direction: 'recvonly' });
          if (videoTransceiver && videoTransceiver.receiver) {
            this.applyZeroLatencyPlayoutDelay(videoTransceiver.receiver, 'video');
          }
        }

        pc.ontrack = (event) => {
          if (session.cancelled) return;
          if (event.receiver && event.track && event.track.kind === 'video') {
            this.applyZeroLatencyPlayoutDelay(event.receiver, 'video');
          }
          if (event.streams && event.streams[0]) {
            session.element.srcObject = event.streams[0];
          } else {
            const MediaStreamConstructor = typeof MediaStream !== 'undefined' ? MediaStream : (typeof window !== 'undefined' ? window.MediaStream : null);
            if (!session.element.srcObject && MediaStreamConstructor) {
              session.element.srcObject = new MediaStreamConstructor();
            }
            if (session.element.srcObject && typeof session.element.srcObject.addTrack === 'function' && event.track) {
              session.element.srcObject.addTrack(event.track);
            }
          }
        };

        const onConnChange = () => this.handleWebRTCConnectionChange(session);
        if (typeof pc.addEventListener === 'function') {
          pc.addEventListener('iceconnectionstatechange', onConnChange);
          pc.addEventListener('connectionstatechange', onConnChange);
        }
        pc.oniceconnectionstatechange = onConnChange;
        pc.onconnectionstatechange = onConnChange;

        await this.negotiateWebRTC(session, session.stream.stream_url, session.element);
      } catch (err) {
        console.warn('[MirrormereVideo] WebRTC reconnection cycle error:', err);
        if (!session.cancelled) {
          this.scheduleWebRTCReconnect(session);
        }
      }
    }

    /**
     * Handles stream failure when reconnect retries are exhausted.
     * Drops cleanly back to widgets mode if primary stream fails, or dismisses PiP slot.
     */
    handleStreamFallback(session) {
      if (!session || session.cancelled) return;

      const isPrimary = (session === this.primarySession);
      const isPip = (session === this.pipSession);
      const isVisualPrimary = this.isSwapped ? isPip : isPrimary;

      if (isVisualPrimary) {
        console.warn('[MirrormereVideo] Primary presentation stream lost. Falling back to widgets mode.');
        this.exitVideoMode();
      } else {
        console.warn('[MirrormereVideo] PiP stream lost. Tearing down PiP slot.');
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
        this.syncAudioFocus();
      }

      if (typeof this.options.onFallback === 'function') {
        this.options.onFallback(session, isVisualPrimary ? 'widgets' : 'pip_dismissed');
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

      if (session.spinnerSafetyTimer) {
        clearTimeout(session.spinnerSafetyTimer);
        session.spinnerSafetyTimer = null;
      }

      if (session.spinner && session.spinner.parentNode) {
        session.spinner.parentNode.removeChild(session.spinner);
        session.spinner = null;
      }

      if (session.reconnectTimer) {
        clearTimeout(session.reconnectTimer);
        session.reconnectTimer = null;
      }
      if (session.disconnectTimer) {
        clearTimeout(session.disconnectTimer);
        session.disconnectTimer = null;
      }

      if (session.touchCleanups) {
        session.touchCleanups.forEach(cleanup => cleanup());
        session.touchCleanups = [];
      }

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
        session.pc.oniceconnectionstatechange = null;
        session.pc.onconnectionstatechange = null;
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
     * Maps DOM pointer/touch event coordinates into normalized [0.0, 1.0] video space,
     * accounting for letterboxing/pillarboxing with object-fit: contain.
     * @param {PointerEvent|MouseEvent|TouchEvent} e
     * @param {HTMLVideoElement|HTMLImageElement} videoEl
     * @returns {{x: number, y: number}|null}
     */
    getNormalizedVideoCoordinates(e, videoEl) {
      if (!videoEl || typeof videoEl.getBoundingClientRect !== 'function') return null;
      const rect = videoEl.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) return null;

      let clientX = null;
      let clientY = null;
      if (e.touches && e.touches.length > 0) {
        clientX = e.touches[0].clientX;
        clientY = e.touches[0].clientY;
      } else if (e.changedTouches && e.changedTouches.length > 0) {
        clientX = e.changedTouches[0].clientX;
        clientY = e.changedTouches[0].clientY;
      } else if (typeof e.clientX === 'number' && typeof e.clientY === 'number') {
        clientX = e.clientX;
        clientY = e.clientY;
      }

      if (clientX === null || clientY === null) return null;

      let contentWidth = rect.width;
      let contentHeight = rect.height;
      let contentLeft = rect.left;
      let contentTop = rect.top;

      // Compensate for letterboxing or pillarboxing with object-fit: contain
      if (videoEl.videoWidth > 0 && videoEl.videoHeight > 0) {
        const videoRatio = videoEl.videoWidth / videoEl.videoHeight;
        const elementRatio = rect.width / rect.height;

        if (elementRatio > videoRatio) {
          // Pillarboxed: black bars on left and right
          contentWidth = rect.height * videoRatio;
          contentLeft = rect.left + (rect.width - contentWidth) / 2;
        } else {
          // Letterboxed: black bars on top and bottom
          contentHeight = rect.width / videoRatio;
          contentTop = rect.top + (rect.height - contentHeight) / 2;
        }
      }

      const relX = (clientX - contentLeft) / contentWidth;
      const relY = (clientY - contentTop) / contentHeight;

      // Clamp coordinates to [0.0, 1.0]
      return {
        x: Math.max(0.0, Math.min(1.0, relX)),
        y: Math.max(0.0, Math.min(1.0, relY)),
      };
    }

    /**
     * Binds pointer event handlers to the video element for browser-level touch forwarding.
     * @param {Object} session - Active video stream session.
     */
    bindTouchForwarding(session) {
      if (!this.touchForwardingEnabled || !session || !session.element) return;
      const videoEl = session.element;

      let isDown = false;
      let startX = 0;
      let startY = 0;
      let lastMoveTime = 0;
      let hasMoved = false;

      const isPresentationPrimary = () => {
        return (session === this.primarySession && !this.isSwapped) ||
               (session === this.pipSession && this.isSwapped);
      };

      const onPointerDown = (e) => {
        if (!isPresentationPrimary()) return;
        if (e.button !== undefined && e.button !== 0) return;

        const coords = this.getNormalizedVideoCoordinates(e, videoEl);
        if (!coords) return;

        isDown = true;
        hasMoved = false;
        startX = e.clientX !== undefined ? e.clientX : 0;
        startY = e.clientY !== undefined ? e.clientY : 0;
        lastMoveTime = Date.now();

        if (typeof e.preventDefault === 'function') e.preventDefault();
        if (typeof e.stopPropagation === 'function') e.stopPropagation();

        this.sendTouchEvent('down', coords.x, coords.y);
      };

      const onPointerMove = (e) => {
        if (!isDown) return;
        if (!isPresentationPrimary()) return;

        const currentX = e.clientX !== undefined ? e.clientX : 0;
        const currentY = e.clientY !== undefined ? e.clientY : 0;
        const dist = Math.hypot(currentX - startX, currentY - startY);
        if (dist > 10) {
          hasMoved = true;
        }

        const now = Date.now();
        if (now - lastMoveTime < 30) return; // ~33Hz throttle
        lastMoveTime = now;

        const coords = this.getNormalizedVideoCoordinates(e, videoEl);
        if (!coords) return;

        if (typeof e.preventDefault === 'function') e.preventDefault();
        if (typeof e.stopPropagation === 'function') e.stopPropagation();

        this.sendTouchEvent('move', coords.x, coords.y);
      };

      const onPointerUp = (e) => {
        if (!isDown) return;
        isDown = false;
        if (!isPresentationPrimary()) return;

        const coords = this.getNormalizedVideoCoordinates(e, videoEl);

        if (typeof e.preventDefault === 'function') e.preventDefault();
        if (typeof e.stopPropagation === 'function') e.stopPropagation();

        if (!hasMoved && coords) {
          this.sendTouchEvent('tap', coords.x, coords.y);
        } else if (coords) {
          this.sendTouchEvent('up', coords.x, coords.y);
        }
      };

      const onPointerCancel = (e) => {
        if (!isDown) return;
        isDown = false;
        if (!isPresentationPrimary()) return;

        const coords = this.getNormalizedVideoCoordinates(e, videoEl);
        const x = coords ? coords.x : 0.5;
        const y = coords ? coords.y : 0.5;
        this.sendTouchEvent('up', x, y);
      };

      const onWindowBlur = () => {
        if (!isDown) return;
        isDown = false;
        const coords = this.getNormalizedVideoCoordinates({ clientX: startX, clientY: startY }, videoEl);
        const x = coords ? coords.x : 0.5;
        const y = coords ? coords.y : 0.5;
        this.sendTouchEvent('up', x, y);
      };

      videoEl.addEventListener('pointerdown', onPointerDown);
      videoEl.addEventListener('pointermove', onPointerMove);
      videoEl.addEventListener('pointerup', onPointerUp);
      videoEl.addEventListener('pointercancel', onPointerCancel);
      videoEl.addEventListener('pointerleave', onPointerCancel);

      if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
        window.addEventListener('blur', onWindowBlur);
      }

      session.touchCleanups = [
        () => videoEl.removeEventListener('pointerdown', onPointerDown),
        () => videoEl.removeEventListener('pointermove', onPointerMove),
        () => videoEl.removeEventListener('pointerup', onPointerUp),
        () => videoEl.removeEventListener('pointercancel', onPointerCancel),
        () => videoEl.removeEventListener('pointerleave', onPointerCancel),
        () => {
          if (typeof window !== 'undefined' && typeof window.removeEventListener === 'function') {
            window.removeEventListener('blur', onWindowBlur);
          }
        },
      ];
    }

    /**
     * Dispatches normalized touch actions to the remote sidecar via HTTP POST.
     * @param {'down'|'move'|'up'|'tap'} action
     * @param {number} x
     * @param {number} y
     * @returns {Promise<any>}
     */
    sendTouchEvent(action, x, y) {
      const fetchFn = this.fetchFn || (typeof fetch === 'function' ? fetch : null);
      if (!fetchFn || !this.remoteUrl) return Promise.resolve();

      const payload = { action, x, y };
      return fetchFn(`${this.remoteUrl}/touch`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
        keepalive: true,
      }).catch(() => {
        // Fallback to /tap for older sidecar versions
        if (action === 'tap') {
          return fetchFn(`${this.remoteUrl}/tap`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ x, y }),
            keepalive: true,
          }).catch(() => {});
        }
      });
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
      if (this.paintFlushTimer) {
        const cancelRAF = (typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function')
          ? window.cancelAnimationFrame.bind(window)
          : (typeof cancelAnimationFrame === 'function' ? cancelAnimationFrame : clearTimeout);
        cancelRAF(this.paintFlushTimer);
        this.paintFlushTimer = null;
      }
      this.exitVideoMode(false);
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
