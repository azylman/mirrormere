/**
 * Mirrormere Display Application Runtime
 * Bootstraps persistent header (clock, weather pill, system status),
 * SSE subscriptions, stylesheet hot-reloading, and screen carousel.
 * Complies with SPEC-001, SPEC-003, SPEC-006, SPEC-010, and SPEC-012.
 */
(() => {
  let sseClient = null;
  let carousel = null;
  let currentTimezone = null;

  /**
   * Validate IANA timezone string.
   */
  function isValidTimezone(tz) {
    if (!tz || typeof tz !== 'string') return false;
    if (tz.includes('{{') || tz.includes('}}')) return false;
    try {
      new Intl.DateTimeFormat(undefined, { timeZone: tz });
      return true;
    } catch {
      return false;
    }
  }

  /**
   * Resolve active household timezone from DOM data-timezone attributes or runtime state.
   */
  function getTimezone() {
    if (currentTimezone) return currentTimezone;

    if (typeof document !== 'undefined') {
      const appEl = document.getElementById('mirrormere-app');
      if (appEl && appEl.dataset && isValidTimezone(appEl.dataset.timezone)) {
        currentTimezone = appEl.dataset.timezone;
        return currentTimezone;
      }

      if (document.body && document.body.dataset && isValidTimezone(document.body.dataset.timezone)) {
        currentTimezone = document.body.dataset.timezone;
        return currentTimezone;
      }

      const clockEl = document.getElementById('header-clock');
      if (clockEl && clockEl.dataset && isValidTimezone(clockEl.dataset.timezone)) {
        currentTimezone = clockEl.dataset.timezone;
        return currentTimezone;
      }
    }

    return undefined;
  }

  /**
   * Set active household timezone and update clock immediately.
   */
  function setTimezone(tz) {
    if (isValidTimezone(tz)) {
      currentTimezone = tz;
      if (typeof document !== 'undefined') {
        const appEl = document.getElementById('mirrormere-app');
        if (appEl && appEl.dataset) appEl.dataset.timezone = tz;
        if (document.body && document.body.dataset) document.body.dataset.timezone = tz;
        const clockEl = document.getElementById('header-clock');
        if (clockEl && clockEl.dataset) clockEl.dataset.timezone = tz;
      }
      updateClock();
    }
  }

  /**
   * Format digital time respecting household timezone and client locale.
   */
  function formatTime(date, tz) {
    const opts = { hour: 'numeric', minute: '2-digit' };
    if (isValidTimezone(tz)) opts.timeZone = tz;
    return new Intl.DateTimeFormat(undefined, opts).format(date);
  }

  /**
   * Format date respecting household timezone and client locale.
   */
  function formatDate(date, tz) {
    const opts = { weekday: 'long', month: 'short', day: 'numeric' };
    if (isValidTimezone(tz)) opts.timeZone = tz;
    return new Intl.DateTimeFormat(undefined, opts).format(date);
  }

  /**
   * Update header clock and date.
   */
  function updateClock(customInstant) {
    if (typeof document === 'undefined') return;
    const clockEl = document.getElementById('header-clock');
    const dateEl = document.getElementById('header-date');
    if (!clockEl || !dateEl) return;

    const now = customInstant instanceof Date ? customInstant : new Date();
    const tz = getTimezone();

    try {
      clockEl.textContent = formatTime(now, tz);
      dateEl.textContent = formatDate(now, tz);
    } catch {
      // Graceful fallback to unzoned locale format if timezone is invalid
      clockEl.textContent = formatTime(now, undefined);
      dateEl.textContent = formatDate(now, undefined);
    }
  }

  /**
   * Update header ambient weather pill badge and household timezone (header.update event).
   */
  function handleHeaderUpdate(data) {
    if (!data) return;

    if (data.timezone && isValidTimezone(data.timezone)) {
      setTimezone(data.timezone);
    }

    if (data.weather) {
      const weather = data.weather;
      const tempEl = document.getElementById('weather-temp');
      const conditionEl = document.getElementById('weather-condition');

      if (tempEl && weather.temperature !== undefined) {
        const units = weather.units || '°';
        const unitLabel = units.includes('°') ? units : `${units}`;
        tempEl.textContent = `${Math.round(weather.temperature)}°${unitLabel.replace('°', '')}`;
      }

      if (conditionEl) {
        const condition = weather.icon || weather.condition || '';
        conditionEl.textContent = condition;
      }
    }
  }

  /**
   * Update system status indicator dot (system.status event).
   */
  function handleSystemStatus(data) {
    if (typeof document === 'undefined') return;
    const dot = document.getElementById('system-status-dot');
    if (!dot || !data) return;

    dot.classList.remove('status-ok', 'status-degraded', 'status-error');

    if (data.online === false) {
      dot.classList.add('status-error');
      dot.title = 'System Status: Offline';
    } else if (data.config_status === 'error') {
      dot.classList.add('status-degraded');
      dot.title = `Configuration Degraded: ${data.config_error || 'Error'}`;
    } else {
      dot.classList.add('status-ok');
      dot.title = 'System Status: Healthy';
    }
  }

  /**
   * Hot-swap stylesheet link on style.reload event with zero visual flicker.
   */
  function handleStyleReload(data) {
    if (typeof document === 'undefined') return;
    const link = document.getElementById('hud-stylesheet') || document.querySelector('link[rel="stylesheet"][href*="/style.css"]');
    if (!link) return;

    const cacheBuster = `t=${Date.now()}`;
    const baseHref = link.href.split('?')[0];
    link.href = `${baseHref}?${cacheBuster}`;
    console.log(`[MirrormereDisplay] Hot-swapped stylesheet: ${data && data.file ? data.file : 'custom.css'}`);
  }

  let clockTimer = null;

  /**
   * Initialize display client application.
   */
  function init() {
    if (typeof document === 'undefined') return;

    // 1. Start real-time clock
    updateClock();
    if (clockTimer) clearInterval(clockTimer);
    clockTimer = setInterval(updateClock, 1000);
    if (clockTimer && typeof clockTimer.unref === 'function') {
      clockTimer.unref();
    }

    // 2. Initialize Carousel controller
    const canvasEl = document.getElementById('grid-canvas');
    if (typeof window !== 'undefined' && window.MirrormereCarousel) {
      carousel = new window.MirrormereCarousel(canvasEl);
    }

    // 3. Initialize SSE client
    if (typeof window !== 'undefined' && window.MirrormereSSE) {
      sseClient = new window.MirrormereSSE('/api/events');

      // 4. Initialize Audio Manager
      if (window.MirrormereAudio && window.MirrormereAudio.AudioManager) {
        window.audioManager = new window.MirrormereAudio.AudioManager({ sseClient });
      }

      // 5. Initialize Video Player Manager (SPEC-004 §1, §5, SPEC-010 §1)
      if (window.MirrormereVideo && window.MirrormereVideo.VideoPlayerManager) {
        const stageEl = document.getElementById('video-stage');
        const primaryEl = document.getElementById('video-primary-slot');
        const pipEl = document.getElementById('video-pip-slot');
        window.videoManager = new window.MirrormereVideo.VideoPlayerManager({
          stageElement: stageEl,
          primarySlot: primaryEl,
          pipSlot: pipEl,
          audioManager: window.audioManager,
          carousel: carousel,
        });

        sseClient.on('video.state', (data) => {
          if (window.videoManager) {
            window.videoManager.handleVideoState(data);
          }
        });
      }

      sseClient.on('screen.rotate', (data) => {
        if (carousel) carousel.handleScreenRotate(data);
      });

      sseClient.on('widget.update', (data) => {
        if (carousel) carousel.handleWidgetUpdate(data);
      });

      sseClient.on('widget.reload', (data) => {
        if (carousel) carousel.handleWidgetReload(data);
      });

      sseClient.on('style.reload', (data) => {
        handleStyleReload(data);
      });

      sseClient.on('header.update', (data) => {
        handleHeaderUpdate(data);
      });

      sseClient.on('system.status', (data) => {
        handleSystemStatus(data);
      });

      sseClient.connect();
    }
  }

  if (typeof document !== 'undefined' && (typeof module === 'undefined' || !module.exports)) {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', init);
    } else {
      init();
    }
  }

  const api = {
    updateClock,
    handleHeaderUpdate,
    handleSystemStatus,
    handleStyleReload,
    getTimezone,
    setTimezone,
    formatTime,
    formatDate,
    isValidTimezone,
    init,
  };

  if (typeof window !== 'undefined') {
    window.MirrormereDisplay = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
})();
