/**
 * Mirrormere Display Application Runtime
 * Bootstraps persistent header (clock, weather pill, system status),
 * SSE subscriptions, stylesheet hot-reloading, and screen carousel.
 * Complies with SPEC-003, SPEC-006, and SPEC-010.
 */
(() => {
  let sseClient = null;
  let carousel = null;

  /**
   * Update header clock and date.
   */
  function updateClock() {
    const clockEl = document.getElementById('header-clock');
    const dateEl = document.getElementById('header-date');
    if (!clockEl || !dateEl) return;

    const now = new Date();

    // 12-hour format with AM/PM (e.g. 10:24 AM)
    let hours = now.getHours();
    const minutes = String(now.getMinutes()).padStart(2, '0');
    const ampm = hours >= 12 ? 'PM' : 'AM';
    hours = hours % 12;
    hours = hours ? hours : 12; // 0 becomes 12
    clockEl.textContent = `${hours}:${minutes} ${ampm}`;

    // Date: Weekday, Month Day (e.g. Friday, Sep 25)
    const options = { weekday: 'long', month: 'short', day: 'numeric' };
    dateEl.textContent = now.toLocaleDateString(undefined, options);
  }

  /**
   * Update header ambient weather pill badge (header.update event).
   */
  function handleHeaderUpdate(data) {
    if (!data || !data.weather) return;

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

  /**
   * Update system status indicator dot (system.status event).
   */
  function handleSystemStatus(data) {
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
    const link = document.getElementById('hud-stylesheet') || document.querySelector('link[rel="stylesheet"][href*="/style.css"]');
    if (!link) return;

    const cacheBuster = `t=${Date.now()}`;
    const baseHref = link.href.split('?')[0];
    link.href = `${baseHref}?${cacheBuster}`;
    console.log(`[MirrormereDisplay] Hot-swapped stylesheet: ${data && data.file ? data.file : 'custom.css'}`);
  }

  /**
   * Initialize display client application.
   */
  function init() {
    // 1. Start real-time clock
    updateClock();
    setInterval(updateClock, 1000);

    // 2. Initialize Carousel controller
    const canvasEl = document.getElementById('grid-canvas');
    if (window.MirrormereCarousel) {
      carousel = new window.MirrormereCarousel(canvasEl);
    }

    // 3. Initialize SSE client
    if (window.MirrormereSSE) {
      sseClient = new window.MirrormereSSE('/api/events');

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

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
