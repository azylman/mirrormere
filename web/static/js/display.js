/**
 * Mirrormere Display Application Runtime
 * Bootstraps persistent header (clock, weather pill, system status),
 * SSE subscriptions, stylesheet hot-reloading, and screen carousel.
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
    const now = customInstant instanceof Date ? customInstant : new Date();
    checkDailyMaintenanceReload(now);

    const clockEl = document.getElementById('header-clock');
    const dateEl = document.getElementById('header-date');
    if (!clockEl || !dateEl) return;
    const tz = getTimezone();

    try {
      const timeStr = formatTime(now, tz);
      clockEl.textContent = timeStr;
      const m = timeStr.match(/^(\d{1,2}:\d{2})\s*([AP]M)?$/i);
      if (m && m[2]) {
        clockEl.innerHTML = `${m[1]}<span class="header-clock-ampm">${m[2]}</span>`;
      }

      const dateStr = formatDate(now, tz);
      dateEl.textContent = dateStr;
      const dayOpts = { weekday: 'long' };
      if (isValidTimezone(tz)) dayOpts.timeZone = tz;
      const weekday = new Intl.DateTimeFormat(undefined, dayOpts).format(now);

      const fullOpts = { month: 'short', day: 'numeric' };
      if (isValidTimezone(tz)) fullOpts.timeZone = tz;
      const shortDate = new Intl.DateTimeFormat(undefined, fullOpts).format(now);

      dateEl.innerHTML = `<span class="header-date-weekday">${weekday}</span><span class="header-date-full">${shortDate}</span>`;
    } catch {
      // Graceful fallback to unzoned locale format if timezone is invalid
      clockEl.textContent = formatTime(now, undefined);
      dateEl.textContent = formatDate(now, undefined);
    }
  }

  const WEATHER_ICON_PATHS = {
    'weather-sunny': '<circle cx="12" cy="12" r="5" fill="#f59e0b" stroke="#f59e0b" stroke-width="1"/><g stroke="#f59e0b" stroke-width="2" stroke-linecap="round"><line x1="12" y1="1.5" x2="12" y2="3.5"/><line x1="12" y1="20.5" x2="12" y2="22.5"/><line x1="4.5" y1="4.5" x2="6" y2="6"/><line x1="18" y1="18" x2="19.5" y2="19.5"/><line x1="1.5" y1="12" x2="3.5" y2="12"/><line x1="20.5" y1="12" x2="22.5" y2="12"/><line x1="4.5" y1="19.5" x2="6" y2="18"/><line x1="18" y1="6" x2="19.5" y2="4.5"/></g>',
    'weather-partly-cloudy': '<circle cx="16" cy="9" r="4" fill="#f59e0b"/><g stroke="#f59e0b" stroke-width="1.8" stroke-linecap="round"><line x1="16" y1="2" x2="16" y2="3.5"/><line x1="21.5" y1="6" x2="20" y2="7"/><line x1="21.5" y1="12" x2="20" y2="11"/><line x1="11.5" y1="4" x2="12.5" y2="5"/></g><path d="M15.5 19H7a4.5 4.5 0 0 1-.8-8.9A5.5 5.5 0 0 1 16.8 11 3.5 3.5 0 0 1 15.5 19z" fill="#ffffff" stroke="#94a3b8" stroke-width="1.6" stroke-linejoin="round"/>',
    'weather-cloudy': '<path d="M17.5 19H7a5 5 0 0 1-1-9.9A6 6 0 0 1 17.8 11 4 4 0 0 1 17.5 19z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/>',
    'weather-fog': '<g stroke="#94a3b8" stroke-width="2" stroke-linecap="round"><line x1="4" y1="10" x2="20" y2="10"/><line x1="3" y1="14" x2="21" y2="14"/><line x1="5" y1="18" x2="19" y2="18"/><line x1="7" y1="6" x2="17" y2="6"/></g>',
    'weather-rainy': '<path d="M17.5 15H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 7 3.5 3.5 0 0 1 17.5 15z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g stroke="#38bdf8" stroke-width="2" stroke-linecap="round"><line x1="8" y1="18" x2="7" y2="21"/><line x1="12" y1="18" x2="11" y2="21"/><line x1="16" y1="18" x2="15" y2="21"/></g>',
    'weather-pouring': '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><g stroke="#0284c7" stroke-width="2" stroke-linecap="round"><line x1="6" y1="17" x2="4" y2="21"/><line x1="10" y1="17" x2="8" y2="21"/><line x1="14" y1="17" x2="12" y2="21"/><line x1="18" y1="17" x2="16" y2="21"/></g>',
    'weather-snowy': '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g fill="#93c5fd"><circle cx="8" cy="18" r="1.3"/><circle cx="12" cy="18" r="1.3"/><circle cx="16" cy="18" r="1.3"/><circle cx="10" cy="21" r="1.3"/><circle cx="14" cy="21" r="1.3"/></g>',
    'weather-snowy-rainy': '<path d="M17.5 14H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 6 3.5 3.5 0 0 1 17.5 14z" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.7" stroke-linejoin="round"/><g fill="#93c5fd"><circle cx="8" cy="18" r="1.3"/><circle cx="14" cy="18" r="1.3"/></g><g stroke="#38bdf8" stroke-width="2" stroke-linecap="round"><line x1="11" y1="18" x2="10" y2="21"/></g>',
    'weather-lightning': '<path d="M17.5 13H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 5 3.5 3.5 0 0 1 17.5 13z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><polygon points="12 13 9 18 13 18 11 23 16 16 12 16 14 13" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linejoin="round"/>',
    'weather-lightning-rainy': '<path d="M17.5 13H7a4.5 4.5 0 0 1-1-8.9A5.5 5.5 0 0 1 16.8 5 3.5 3.5 0 0 1 17.5 13z" fill="#e2e8f0" stroke="#64748b" stroke-width="1.7" stroke-linejoin="round"/><polygon points="12 13 9 18 13 18 11 23 16 16 12 16 14 13" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linejoin="round"/>',
  };

  const WEATHER_DESC_MAP = {
    'weather-sunny': 'Sunny & Mild',
    'weather-partly-cloudy': 'Partly Cloudy',
    'weather-cloudy': 'Overcast',
    'weather-fog': 'Misty & Foggy',
    'weather-rainy': 'Light Rain',
    'weather-pouring': 'Heavy Rain',
    'weather-snowy': 'Snow Showers',
    'weather-snowy-rainy': 'Rain & Snow',
    'weather-lightning': 'Thunderstorm',
    'weather-lightning-rainy': 'Severe Storm',
  };

  /**
   * Return inline SVG markup for a WMO weather condition token.
   */
  function getWeatherIconSVG(token, size = 24) {
    const key = token && WEATHER_ICON_PATHS[token] ? token : 'weather-cloudy';
    const path = WEATHER_ICON_PATHS[key] || WEATHER_ICON_PATHS['weather-cloudy'];
    const safeToken = key.replace(/[^a-z0-9_-]/gi, '');
    return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" class="mm-weather-icon mm-icon-${safeToken}">${path}</svg>`;
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
      const locationEl = document.getElementById('weather-location');
      const descEl = document.getElementById('weather-desc');

      if (tempEl && weather.temperature !== undefined) {
        const units = weather.units || '°';
        const unitLabel = units.includes('°') ? units : `${units}`;
        tempEl.textContent = `${Math.round(weather.temperature)}°${unitLabel.replace('°', '')}`;
      }

      const conditionToken = weather.icon || weather.condition || '';
      if (conditionEl) {
        conditionEl.innerHTML = getWeatherIconSVG(conditionToken, 24);
        conditionEl.title = conditionToken;
      }

      if (locationEl) {
        locationEl.textContent = weather.location || 'Oakland';
      }

      if (descEl) {
        if (weather.description) {
          descEl.textContent = weather.description;
        } else if (WEATHER_DESC_MAP[conditionToken]) {
          descEl.textContent = WEATHER_DESC_MAP[conditionToken];
        } else if (weather.condition && WEATHER_DESC_MAP[weather.condition]) {
          descEl.textContent = WEATHER_DESC_MAP[weather.condition];
        } else if (weather.condition && weather.condition !== conditionToken) {
          descEl.textContent = weather.condition;
        } else {
          descEl.textContent = 'Sunny & Mild';
        }
      }
    }
  }

  let currentBootId = null;
  let lastReloadTimestamp = 0;
  let lastDailyReloadDate = '';
  let pendingReloadTimer = null;
  let pendingBootId = null;

  const STORAGE_KEY_DAILY_RELOAD = 'mm_last_daily_reload_date';
  const STORAGE_KEY_DEPLOY_RELOAD = 'mm_last_reload_timestamp';

  function getSessionStorageItem(key) {
    try {
      if (typeof window !== 'undefined' && window.sessionStorage) {
        return window.sessionStorage.getItem(key);
      }
    } catch (_) {}
    return null;
  }

  function setSessionStorageItem(key, val) {
    try {
      if (typeof window !== 'undefined' && window.sessionStorage) {
        window.sessionStorage.setItem(key, String(val));
      }
    } catch (_) {}
  }

  /**
   * Check and execute daily maintenance reload at 4:00 AM household time.
   * Flushes long-lived DOM buffers, GPU memory, and browser cache while kiosk is idle.
   */
  function checkDailyMaintenanceReload(customInstant) {
    const now = customInstant instanceof Date ? customInstant : new Date();
    const tz = getTimezone();
    let hour = now.getHours();
    let minute = now.getMinutes();
    let dateStr = now.toDateString();

    if (isValidTimezone(tz)) {
      try {
        const parts = new Intl.DateTimeFormat('en-US', {
          timeZone: tz,
          hour: 'numeric',
          minute: 'numeric',
          hour12: false,
          year: 'numeric',
          month: 'numeric',
          day: 'numeric',
        }).formatToParts(now);

        for (const p of parts) {
          if (p.type === 'hour') hour = parseInt(p.value, 10);
          if (p.type === 'minute') minute = parseInt(p.value, 10);
        }
        dateStr = new Intl.DateTimeFormat('en-US', { timeZone: tz }).format(now);
      } catch (err) {
        console.warn(`[MirrormereDisplay] Failed to format timezone ${tz} for maintenance check:`, err);
      }
    }

    const storedDailyDate = getSessionStorageItem(STORAGE_KEY_DAILY_RELOAD);
    const effectiveDailyDate = lastDailyReloadDate || storedDailyDate || '';

    if (hour === 4 && minute === 0 && effectiveDailyDate !== dateStr) {
      lastDailyReloadDate = dateStr;
      setSessionStorageItem(STORAGE_KEY_DAILY_RELOAD, dateStr);
      console.log(`[MirrormereDisplay] Executing scheduled 4:00 AM maintenance reload for ${dateStr}...`);
      if (typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
        window.location.reload();
      }
      return true;
    }
    return false;
  }

  /**
   * Update system status indicator dot and detect backend deployments (system.status event).
   */
  function handleSystemStatus(data) {
    if (typeof document === 'undefined') return;
    const dot = document.getElementById('system-status-dot');
    if (dot && data) {
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

    // Server deployment detection: reload if boot_id changes after initial connection
    if (data && data.boot_id) {
      if (currentBootId === null) {
        currentBootId = data.boot_id;
      } else if (currentBootId !== data.boot_id) {
        const now = Date.now();
        const storedTs = parseInt(getSessionStorageItem(STORAGE_KEY_DEPLOY_RELOAD) || '0', 10);
        const effectiveLastReload = Math.max(lastReloadTimestamp, isNaN(storedTs) ? 0 : storedTs);
        const elapsed = now - effectiveLastReload;

        // Prevent reload storm if server is in crash-loop (minimum 30 seconds cooldown)
        if (elapsed >= 30000) {
          if (pendingReloadTimer) {
            clearTimeout(pendingReloadTimer);
            pendingReloadTimer = null;
          }
          pendingBootId = null;
          lastReloadTimestamp = now;
          setSessionStorageItem(STORAGE_KEY_DEPLOY_RELOAD, String(now));
          currentBootId = data.boot_id;
          console.log(`[MirrormereDisplay] Server deployment detected (${data.boot_id}), reloading page...`);
          if (typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
            window.location.reload();
          }
        } else {
          // Defer reload until cooldown expires so rapid restarts/deploys are not dropped
          pendingBootId = data.boot_id;
          if (!pendingReloadTimer) {
            const delay = Math.max(0, 30000 - elapsed);
            console.warn(`[MirrormereDisplay] Rapid deployment reload throttled (< 30s), deferred for ${delay}ms`);
            pendingReloadTimer = setTimeout(() => {
              pendingReloadTimer = null;
              const targetBootId = pendingBootId;
              pendingBootId = null;
              if (targetBootId && targetBootId !== currentBootId) {
                const reloadNow = Date.now();
                lastReloadTimestamp = reloadNow;
                setSessionStorageItem(STORAGE_KEY_DEPLOY_RELOAD, String(reloadNow));
                currentBootId = targetBootId;
                console.log(`[MirrormereDisplay] Deferred server deployment reload executing (${targetBootId})...`);
                if (typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
                  window.location.reload();
                }
              }
            }, delay);
            if (pendingReloadTimer && typeof pendingReloadTimer.unref === 'function') {
              pendingReloadTimer.unref();
            }
          } else {
            console.warn(`[MirrormereDisplay] Rapid deployment reload throttled (< 30s), updated pending boot_id to ${data.boot_id}`);
          }
        }
      } else if (pendingReloadTimer && data.boot_id === currentBootId) {
        // If server boot_id reverted to currentBootId before timer fired, cancel deferred reload
        clearTimeout(pendingReloadTimer);
        pendingReloadTimer = null;
        pendingBootId = null;
      }
    }
  }

  function getBootIdState() {
    const storedDailyDate = getSessionStorageItem(STORAGE_KEY_DAILY_RELOAD);
    const storedTs = parseInt(getSessionStorageItem(STORAGE_KEY_DEPLOY_RELOAD) || '0', 10);
    return {
      currentBootId,
      lastReloadTimestamp: Math.max(lastReloadTimestamp, isNaN(storedTs) ? 0 : storedTs),
      lastDailyReloadDate: lastDailyReloadDate || storedDailyDate || '',
      pendingBootId,
      hasPendingReloadTimer: pendingReloadTimer !== null,
    };
  }

  function setBootIdState(state = {}) {
    if ('currentBootId' in state) currentBootId = state.currentBootId;
    if ('lastReloadTimestamp' in state) {
      lastReloadTimestamp = state.lastReloadTimestamp;
      setSessionStorageItem(STORAGE_KEY_DEPLOY_RELOAD, state.lastReloadTimestamp);
    }
    if ('lastDailyReloadDate' in state) {
      lastDailyReloadDate = state.lastDailyReloadDate;
      setSessionStorageItem(STORAGE_KEY_DAILY_RELOAD, state.lastDailyReloadDate);
    }
    if ('pendingBootId' in state) pendingBootId = state.pendingBootId;
  }

  function resetBootIdState() {
    currentBootId = null;
    lastReloadTimestamp = 0;
    lastDailyReloadDate = '';
    if (pendingReloadTimer) {
      clearTimeout(pendingReloadTimer);
      pendingReloadTimer = null;
    }
    pendingBootId = null;
    try {
      if (typeof window !== 'undefined' && window.sessionStorage) {
        window.sessionStorage.removeItem(STORAGE_KEY_DAILY_RELOAD);
        window.sessionStorage.removeItem(STORAGE_KEY_DEPLOY_RELOAD);
      }
    } catch (_) {}
  }

  /**
   * Hot-swap stylesheet link on style.reload event with zero visual flicker.
   */
  function handleStyleReload(data) {
    if (typeof document === 'undefined') return;
    const link = document.getElementById('hud-stylesheet') || document.querySelector('link[rel="stylesheet"][href*="style.css"]');
    if (!link) return;

    const cacheBuster = `t=${Date.now()}`;
    const baseHref = link.href.split('?')[0];
    link.href = `${baseHref}?${cacheBuster}`;
    console.log(`[MirrormereDisplay] Hot-swapped stylesheet: ${data && data.file ? data.file : 'custom.css'}`);
  }

  const STYLE_HEARTBEAT_INTERVAL_MS = 120000; // 2 minutes
  let lastStyleModified = null;
  let lastStyleETag = null;
  let styleHeartbeatTimer = null;

  /**
   * Check style.css via conditional HEAD request and hot-swap on change.
   */
  async function checkStyleHeartbeat() {
    if (typeof fetch === 'undefined' || typeof document === 'undefined') return;
    const link = document.getElementById('hud-stylesheet') || document.querySelector('link[rel="stylesheet"][href*="style.css"]');
    if (!link) return;

    const url = link.href.split('?')[0];
    try {
      const headers = {};
      if (lastStyleModified) headers['If-Modified-Since'] = lastStyleModified;
      if (lastStyleETag) headers['If-None-Match'] = lastStyleETag;

      const res = await fetch(url, { method: 'HEAD', headers });
      if (res.status === 200) {
        const newModified = res.headers ? res.headers.get('Last-Modified') : null;
        const newETag = res.headers ? res.headers.get('ETag') : null;
        const hasExisting = Boolean(lastStyleModified || lastStyleETag);
        const isChanged = (lastStyleModified && newModified && lastStyleModified !== newModified) ||
                          (lastStyleETag && newETag && lastStyleETag !== newETag);

        if (hasExisting && isChanged) {
          handleStyleReload({ file: 'style.css (heartbeat)' });
        }
        if (newModified) lastStyleModified = newModified;
        if (newETag) lastStyleETag = newETag;
      }
    } catch (_) {
      // Ignore transient network errors on heartbeat
    }
  }

  function getStyleHeartbeatState() {
    return {
      lastStyleModified,
      lastStyleETag,
      hasTimer: Boolean(styleHeartbeatTimer),
    };
  }

  function resetStyleHeartbeatState() {
    lastStyleModified = null;
    lastStyleETag = null;
    if (styleHeartbeatTimer) {
      clearInterval(styleHeartbeatTimer);
      styleHeartbeatTimer = null;
    }
  }

  let clockTimer = null;
  let headerVideoController = null;

  /**
   * Initialize and bind header video action button.
   */
  function setupHeaderVideo(options = {}) {
    const doc = options.document || (typeof document !== 'undefined' ? document : null);
    const btn = options.button || (doc ? doc.getElementById('header-video-btn') : null);
    if (!btn) return null;

    if (headerVideoController && !options.forceNew) {
      if (options.sseClient && !headerVideoController.hasSSE) headerVideoController.attachSSE(options.sseClient);
      return headerVideoController;
    }
    if (headerVideoController && options.forceNew) headerVideoController.destroy();

    const fetchFn = options.fetch || (typeof window !== 'undefined' && typeof window.fetch === 'function' ? window.fetch.bind(window) : (typeof fetch === 'function' ? fetch : null));
    const sse = options.sseClient || sseClient || (typeof window !== 'undefined' ? window.sseClient : null);
    const win = options.window || (typeof window !== 'undefined' ? window : null);
    let isLoading = false, safetyTimer = null, lastTriggerTime = 0, startX = null, startY = null, sseAttached = false;

    const getCoords = (e) => {
      const t = (e && e.changedTouches && e.changedTouches[0]) || e;
      return t && typeof t.clientX === 'number' ? { x: t.clientX, y: t.clientY } : null;
    };
    const pointerDownHandler = (e) => {
      const c = getCoords(e);
      startX = c ? c.x : null;
      startY = c ? c.y : null;
    };
    const clearLoading = () => {
      if (safetyTimer) { clearTimeout(safetyTimer); safetyTimer = null; }
      isLoading = false;
      btn.classList.remove('loading');
    };

    const triggerVideo = async (e) => {
      if (isLoading) return;
      const now = Date.now(), c = getCoords(e);
      if (startX !== null && startY !== null && c) {
        const dist = Math.hypot(c.x - startX, c.y - startY);
        startX = null; startY = null;
        if (dist >= 15) { lastTriggerTime = now; return; }
      }
      startX = null; startY = null;
      if (now - lastTriggerTime < 350) return;
      lastTriggerTime = now;

      if (win && win.videoManager && win.videoManager.currentMode === 'video') {
        if (win.videoHUD && typeof win.videoHUD.showHUD === 'function') win.videoHUD.showHUD();
        return;
      }

      isLoading = true;
      btn.classList.add('loading');
      btn.classList.remove('error');
      safetyTimer = setTimeout(clearLoading, 4000);

      const streamUrl = options.streamUrl || (win && win.MIRRORMERE_CAST_STREAM_URL) || (btn.dataset && btn.dataset.streamUrl) || '';
      const controlUrl = options.controlUrl || (win && win.MIRRORMERE_CAST_CONTROL_URL) || (btn.dataset && btn.dataset.controlUrl) || '';
      const streamId = options.streamId || (btn.dataset && btn.dataset.streamId) || 'chromecast';
      const streamType = options.type || (btn.dataset && btn.dataset.type) || 'webrtc';

      if (controlUrl && fetchFn) {
        fetchFn(controlUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: streamId, action: 'wake' }),
        }).catch((err) => console.warn('[MirrormereDisplay] Header video wake failed:', err));
      }

      const payload = {
        id: streamId,
        stream_url: streamUrl,
        type: streamType,
        priority: options.priority || 'persistent',
        controllable: options.controllable !== undefined ? options.controllable : true,
      };
      if (controlUrl) {
        payload.control_url = controlUrl;
      }

      try {
        if (fetchFn) {
          const resp = await fetchFn('api/video/trigger', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
          });
          if (resp && resp.ok === false) throw new Error(`HTTP ${resp.status}`);
        }
      } catch (err) {
        console.warn('[MirrormereDisplay] Header video trigger failed:', err);
        clearLoading();
        btn.classList.add('error');
        setTimeout(() => btn.classList.remove('error'), 1500);
      }
    };

    btn.addEventListener('pointerdown', pointerDownHandler);
    btn.addEventListener('pointerup', triggerVideo);
    btn.addEventListener('click', triggerVideo);

    const onVideoState = (data) => {
      const mode = data && (data.mode || (data.primary ? 'video' : 'widgets'));
      if (mode === 'video') clearLoading();
    };
    const attachSSE = (client) => {
      if (!client || sseAttached) return;
      if (typeof client.on === 'function') { client.on('video.state', onVideoState); sseAttached = true; }
      else if (typeof client.addEventListener === 'function') { client.addEventListener('video.state', onVideoState); sseAttached = true; }
    };
    if (sse) attachSSE(sse);

    const controller = {
      button: btn,
      get isLoading() { return isLoading; },
      get hasSSE() { return sseAttached; },
      attachSSE,
      triggerVideo,
      clearLoading,
      destroy() {
        btn.removeEventListener('pointerdown', pointerDownHandler);
        btn.removeEventListener('pointerup', triggerVideo);
        btn.removeEventListener('click', triggerVideo);
        if (sse) {
          if (typeof sse.off === 'function') sse.off('video.state', onVideoState);
          else if (typeof sse.removeEventListener === 'function') sse.removeEventListener('video.state', onVideoState);
        }
        clearLoading();
        if (headerVideoController === controller) headerVideoController = null;
      },
    };
    headerVideoController = controller;
    return controller;
  }

  /**
   * Resolve cursor suppression query overrides (?hide_cursor=true/false, ?cursor=none/visible).
   */
  function resolveCursorSuppression() {
    if (typeof window !== 'undefined' && window.location && window.location.search && typeof document !== 'undefined' && document.body) {
      const urlParams = new URLSearchParams(window.location.search);
      if (urlParams.get('hide_cursor') === 'true' || urlParams.get('cursor') === 'none') {
        document.body.classList.add('mm-touch-display', 'mm-touch-kiosk');
      } else if (urlParams.get('hide_cursor') === 'false' || urlParams.get('cursor') === 'visible') {
        document.body.classList.remove('mm-touch-display', 'mm-touch-kiosk');
      }
    }
  }

  /**
   * Initialize display client application.
   */
  function init() {
    if (typeof document === 'undefined') return;

    // 0. Resolve cursor suppression query overrides
    resolveCursorSuppression();

    // 1. Start real-time clock
    updateClock();
    if (clockTimer) clearInterval(clockTimer);
    clockTimer = setInterval(updateClock, 1000);
    if (clockTimer && typeof clockTimer.unref === 'function') {
      clockTimer.unref();
    }

    // 1b. Start stylesheet freshness heartbeat
    checkStyleHeartbeat();
    if (styleHeartbeatTimer) clearInterval(styleHeartbeatTimer);
    styleHeartbeatTimer = setInterval(checkStyleHeartbeat, STYLE_HEARTBEAT_INTERVAL_MS);
    if (styleHeartbeatTimer && typeof styleHeartbeatTimer.unref === 'function') {
      styleHeartbeatTimer.unref();
    }

    // 2. Initialize Carousel controller
    const canvasEl = document.getElementById('grid-canvas');
    if (typeof window !== 'undefined' && window.MirrormereCarousel) {
      carousel = new window.MirrormereCarousel(canvasEl);
      window.carousel = carousel;
    }

    // 3. Initialize SSE client
    if (typeof window !== 'undefined' && window.MirrormereSSE) {
      sseClient = new window.MirrormereSSE('api/events');
      window.sseClient = sseClient;

      if (window.MirrormereLiveView && typeof window.MirrormereLiveView.attachSSE === 'function') {
        window.MirrormereLiveView.attachSSE(sseClient);
      }

      // 4. Initialize Audio Manager
      if (window.MirrormereAudio && window.MirrormereAudio.AudioManager) {
        window.audioManager = new window.MirrormereAudio.AudioManager({ sseClient });
      }

      // 5. Initialize Video Player Manager
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

      // 6. Initialize Touch Video HUD Controller
      if (window.MirrormereHUD && window.MirrormereHUD.VideoHUDController) {
        const overlayEl = document.getElementById('video-hud-overlay');
        const stageEl = document.getElementById('video-stage');
        const bottomBarEl = document.getElementById('video-hud-bottom-bar');
        const rewindBtn = document.getElementById('video-hud-rewind-btn');
        const forwardBtn = document.getElementById('video-hud-forward-btn');
        const playBtn = document.getElementById('video-hud-play-btn');
        window.videoHUD = new window.MirrormereHUD.VideoHUDController({
          overlayElement: overlayEl,
          stageElement: stageEl,
          bottomBarElement: bottomBarEl,
          rewindBtn: rewindBtn,
          forwardBtn: forwardBtn,
          playBtn: playBtn,
          videoManager: window.videoManager,
          remoteUrl: window.videoManager?.remoteUrl,
        });

        if (window.videoManager && typeof window.videoManager.setHUD === 'function') {
          window.videoManager.setHUD(window.videoHUD);
        }

        sseClient.on('video.state', (data) => {
          if (window.videoHUD) {
            window.videoHUD.handleVideoState(data);
          }
        });

        sseClient.on('audio.state', (data) => {
          if (window.videoHUD) {
            window.videoHUD.handleAudioState(data);
          }
        });
      }

      // 7. Initialize Voice HUD Controller
      if (window.MirrormereVoice && window.MirrormereVoice.VoiceHUDController) {
        window.voiceHUD = new window.MirrormereVoice.VoiceHUDController({
          audioManager: window.audioManager,
          sseClient: sseClient,
        });
      }

      // 8. Initialize Header Video Action Button
      setupHeaderVideo({
        sseClient: sseClient,
      });

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
        checkStyleHeartbeat();
      });

      sseClient.on('connection', (data) => {
        if (data && data.status === 'connected') {
          checkStyleHeartbeat();
        }
      });

      sseClient.on('header.update', (data) => {
        handleHeaderUpdate(data);
      });

      sseClient.on('system.status', (data) => {
        handleSystemStatus(data);
      });

      sseClient.connect();
    } else {
      // Standalone or pre-SSE header video initialization
      setupHeaderVideo();
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
    checkDailyMaintenanceReload,
    getBootIdState,
    setBootIdState,
    resetBootIdState,
    handleStyleReload,
    checkStyleHeartbeat,
    getStyleHeartbeatState,
    resetStyleHeartbeatState,
    getWeatherIconSVG,
    getTimezone,
    setTimezone,
    formatTime,
    formatDate,
    isValidTimezone,
    resolveCursorSuppression,
    setupHeaderVideo,
    getHeaderVideoController: () => headerVideoController,
    init,
  };

  if (typeof window !== 'undefined') {
    window.MirrormereDisplay = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
})();
