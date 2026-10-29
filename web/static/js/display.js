/**
 * Mirrormere Display Application Runtime
 * Bootstraps persistent header (clock, weather pill, system status),
 * SSE subscriptions, stylesheet hot-reloading, and screen carousel.
 */
(() => {
  let sseClient = null;
  let carousel = null;
  let currentTimezone = null;
  const inFlightCompletions = new Set();

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

    try {
      updateNextUpEvent(now);
    } catch {
      // Ignore next event update errors during clock ticks
    }
  }

  const WEATHER_ICON_PATHS = {
    'weather-sunny': '<circle cx="12" cy="12" r="5" fill="#f59e0b" stroke="#f59e0b" stroke-width="1"/><g stroke="#f59e0b" stroke-width="2" stroke-linecap="round"><line x1="12" y1="1.5" x2="12" y2="3.5"/><line x1="12" y1="20.5" x2="12" y2="22.5"/><line x1="4.5" y1="4.5" x2="6" y2="6"/><line x1="18" y1="18" x2="19.5" y2="19.5"/><line x1="1.5" y1="12" x2="3.5" y2="12"/><line x1="20.5" y1="12" x2="22.5" y2="12"/><line x1="4.5" y1="19.5" x2="6" y2="18"/><line x1="18" y1="6" x2="19.5" y2="4.5"/></g>',
    'weather-clear-night': '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
    'weather-partly-cloudy': '<circle cx="16" cy="9" r="4" fill="#f59e0b"/><g stroke="#f59e0b" stroke-width="1.8" stroke-linecap="round"><line x1="16" y1="2" x2="16" y2="3.5"/><line x1="21.5" y1="6" x2="20" y2="7"/><line x1="21.5" y1="12" x2="20" y2="11"/><line x1="11.5" y1="4" x2="12.5" y2="5"/></g><path d="M15.5 19H7a4.5 4.5 0 0 1-.8-8.9A5.5 5.5 0 0 1 16.8 11 3.5 3.5 0 0 1 15.5 19z" fill="#ffffff" stroke="#94a3b8" stroke-width="1.6" stroke-linejoin="round"/>',
    'weather-night-partly-cloudy': '<path d="M19 10.5A6 6 0 0 1 12.5 4a5 5 0 0 0 6.5 6.5z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.5" stroke-linejoin="round"/><path d="M15.5 19H7a4.5 4.5 0 0 1-.8-8.9A5.5 5.5 0 0 1 16.8 11 3.5 3.5 0 0 1 15.5 19z" fill="#ffffff" stroke="#94a3b8" stroke-width="1.6" stroke-linejoin="round"/>',
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
    'weather-clear-night': 'Clear',
    'weather-partly-cloudy': 'Partly Cloudy',
    'weather-night-partly-cloudy': 'Partly Cloudy',
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
          const isNight = conditionToken.includes('night') || (weather.condition && weather.condition.toLowerCase().includes('night'));
          descEl.textContent = isNight ? 'Clear' : (weather.condition || 'Clear Sky');
        }
      }
    }
  }

  const STORAGE_KEY_THEME_OVERRIDE = 'mirrormere_theme_override';
  let lastServerTheme = null;

  function getStoredThemeOverride() {
    try {
      if (typeof window !== 'undefined' && window.localStorage) {
        const val = window.localStorage.getItem(STORAGE_KEY_THEME_OVERRIDE);
        if (val === 'day' || val === 'night') return val;
      }
    } catch (_) {}
    return null;
  }

  function setStoredThemeOverride(val) {
    try {
      if (typeof window !== 'undefined' && window.localStorage) {
        if (val === 'day' || val === 'night') {
          window.localStorage.setItem(STORAGE_KEY_THEME_OVERRIDE, val);
        } else {
          window.localStorage.removeItem(STORAGE_KEY_THEME_OVERRIDE);
        }
      }
    } catch (_) {}
  }

  function applyThemeToDOM(theme) {
    const normalized = (theme === 'night') ? 'night' : 'day';
    if (typeof document !== 'undefined') {
      if (document.documentElement && document.documentElement.dataset) {
        document.documentElement.dataset.theme = normalized;
      }
      if (document.body && document.body.dataset) {
        document.body.dataset.theme = normalized;
      }
      const appEl = document.getElementById('mirrormere-app');
      if (appEl && appEl.dataset) {
        appEl.dataset.theme = normalized;
      }
      if (typeof document.querySelector === 'function') {
        const metaTheme = document.querySelector('meta[name="theme-color"]');
        if (metaTheme && typeof metaTheme.setAttribute === 'function') {
          metaTheme.setAttribute('content', normalized === 'night' ? '#3b364c' : '#f8f5ee');
        }
      }
      const btn = document.getElementById('theme-toggle-btn');
      if (btn) {
        const override = getStoredThemeOverride();
        const modeLabel = normalized === 'night' ? 'Dark Mode' : 'Light Mode';
        const nextLabel = normalized === 'night' ? 'Light Mode' : 'Dark Mode';
        const autoTheme = lastServerTheme || 'day';
        const oppositeOfAuto = autoTheme === 'night' ? 'day' : 'night';
        const isOppositeOverride = override === oppositeOfAuto;
        const actionLabel = !override
          ? nextLabel
          : (isOppositeOverride ? nextLabel : 'Auto Solar');
        if (btn.dataset) {
          btn.dataset.theme = normalized;
          btn.dataset.mode = override ? 'manual' : 'auto';
        }
        if (typeof btn.setAttribute === 'function') {
          btn.setAttribute('aria-label', `Switch to ${actionLabel} (Currently ${modeLabel})`);
        }
        btn.title = override
          ? (isOppositeOverride
            ? `${modeLabel} (Manual Override — Tap for ${nextLabel}, Long-press for Auto Solar)`
            : `${modeLabel} (Manual Override — Tap or Long-press for Auto Solar)`)
          : `${modeLabel} (Auto Solar — Tap for ${nextLabel})`;
      }
    }
  }

  function getTheme() {
    if (typeof document !== 'undefined') {
      if (document.documentElement && document.documentElement.dataset && document.documentElement.dataset.theme) {
        return document.documentElement.dataset.theme === 'night' ? 'night' : 'day';
      }
      if (document.body && document.body.dataset && document.body.dataset.theme) {
        return document.body.dataset.theme === 'night' ? 'night' : 'day';
      }
    }
    return lastServerTheme || 'day';
  }

  function setTheme(theme, persistOverride = true) {
    const normalized = (theme === 'night') ? 'night' : 'day';
    if (persistOverride) {
      setStoredThemeOverride(normalized);
    }
    applyThemeToDOM(normalized);
    return normalized;
  }

  function toggleTheme() {
    const current = getTheme();
    const next = current === 'night' ? 'day' : 'night';
    const override = getStoredThemeOverride();
    if (!override) {
      return setTheme(next, true);
    }
    const autoTheme = lastServerTheme || 'day';
    const oppositeOfAuto = autoTheme === 'night' ? 'day' : 'night';
    if (override === oppositeOfAuto) {
      return setTheme(autoTheme, true);
    }
    return resetThemeToAuto();
  }

  function resetThemeToAuto() {
    setStoredThemeOverride(null);
    const target = lastServerTheme || getTheme() || 'day';
    applyThemeToDOM(target);
    return target;
  }

  function setupThemeToggle() {
    if (typeof document === 'undefined') return;
    if (!lastServerTheme) {
      lastServerTheme = getTheme();
    }
    const override = getStoredThemeOverride();
    if (override) {
      applyThemeToDOM(override);
    } else {
      applyThemeToDOM(lastServerTheme);
    }
    const btn = document.getElementById('theme-toggle-btn');
    if (btn && btn.dataset && !btn.dataset.boundThemeToggle && typeof btn.addEventListener === 'function') {
      btn.dataset.boundThemeToggle = 'true';
      let longPressTimer = null;
      let longPressTriggered = false;
      const clearLongPress = () => {
        if (longPressTimer) {
          clearTimeout(longPressTimer);
          longPressTimer = null;
        }
      };
      btn.addEventListener('pointerdown', () => {
        longPressTriggered = false;
        clearLongPress();
        longPressTimer = setTimeout(() => {
          longPressTimer = null;
          longPressTriggered = true;
          resetThemeToAuto();
        }, 500);
      });
      btn.addEventListener('pointerup', clearLongPress);
      btn.addEventListener('pointercancel', clearLongPress);
      btn.addEventListener('pointerleave', clearLongPress);
      btn.addEventListener('click', (e) => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        if (longPressTriggered) {
          longPressTriggered = false;
          return;
        }
        toggleTheme();
      });
    }
  }

  /**
   * Update active theme ("day" vs "night") on HTML, body, and app container.
   */
  function handleThemeUpdate(data) {
    if (!data || !data.theme) return;
    const theme = (data.theme === 'night') ? 'night' : 'day';
    lastServerTheme = theme;
    const override = getStoredThemeOverride();
    if (override) {
      applyThemeToDOM(override);
      return;
    }
    applyThemeToDOM(theme);
  }

  let currentBootId = null;
  let lastReloadTimestamp = 0;
  let lastDailyReloadDate = '';
  let pendingReloadTimer = null;
  let pendingBootId = null;
  let pendingSystemReloadTimer = null;
  let lastConfigStatus = 'ok';
  let lastConfigError = null;

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
    if (data && typeof data.config_status === 'string') {
      lastConfigStatus = data.config_status;
      lastConfigError = data.config_error || null;
    }
    const dot = document.getElementById('system-status-dot');
    const container = document.getElementById('header-status');
    if (dot && data) {
      dot.classList.remove('status-ok', 'status-degraded', 'status-error');

      if (data.online === false) {
        dot.classList.add('status-error');
        dot.title = 'System Status: Offline';
        if (container && container.style) container.style.display = '';
      } else if (data.config_status === 'error') {
        dot.classList.add('status-degraded');
        dot.title = `Configuration Degraded: ${data.config_error || 'Error'}`;
        if (container && container.style) container.style.display = '';
      } else {
        dot.classList.add('status-ok');
        dot.title = 'System Status: Healthy';
        if (container && container.style) container.style.display = 'none';
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

  /**
   * Handle server-instructed page reload (system.reload event, e.g. shell configuration change).
   * Debounces multiple rapid reload events (1000ms by default) to prevent reload storms.
   */
  function handleSystemReload(data, delayMs = 1000) {
    if (typeof document === 'undefined') return;
    const reason = (data && data.reason) ? data.reason : 'shell_change';
    console.log(`[MirrormereDisplay] Server-instructed reload received (reason: ${reason}), scheduling reload...`);

    if (pendingSystemReloadTimer) {
      clearTimeout(pendingSystemReloadTimer);
      pendingSystemReloadTimer = null;
    }

    if (delayMs <= 0) {
      console.log(`[MirrormereDisplay] Executing immediate server-instructed reload (${reason})...`);
      if (typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
        window.location.reload();
      }
      return;
    }

    pendingSystemReloadTimer = setTimeout(() => {
      pendingSystemReloadTimer = null;
      console.log(`[MirrormereDisplay] Executing server-instructed reload (${reason})...`);
      if (typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
        window.location.reload();
      }
    }, delayMs);

    if (pendingSystemReloadTimer && typeof pendingSystemReloadTimer.unref === 'function') {
      pendingSystemReloadTimer.unref();
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
    lastConfigStatus = 'ok';
    lastConfigError = null;
    if (pendingReloadTimer) {
      clearTimeout(pendingReloadTimer);
      pendingReloadTimer = null;
    }
    if (pendingSystemReloadTimer) {
      clearTimeout(pendingSystemReloadTimer);
      pendingSystemReloadTimer = null;
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
    const link = document.getElementById('hud-stylesheet') || (typeof document.querySelector === 'function' ? document.querySelector('link[rel="stylesheet"][href*="style.css"]') : null);
    if (!link || typeof link.href !== 'string') return;

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
    const link = document.getElementById('hud-stylesheet') || (typeof document.querySelector === 'function' ? document.querySelector('link[rel="stylesheet"][href*="style.css"]') : null);
    if (!link || typeof link.href !== 'string') return;

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
   * Apply optimistic completed styling to a task item DOM element.
   */
  function applyTaskDoneState(taskItem) {
    if (!taskItem) return;
    taskItem.classList.remove('task-item-active');
    taskItem.classList.add('task-item-done');
    const ring = taskItem.querySelector('.task-checkbox-ring');
    if (ring) {
      ring.className = 'task-checkbox-done';
      ring.setAttribute('aria-hidden', 'true');
      ring.innerHTML = `<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"></polyline></svg>`;
    }
  }

  /**
   * Revert optimistic completed styling back to active on a task item DOM element.
   */
  function revertTaskDoneState(taskItem) {
    if (!taskItem) return;
    taskItem.classList.remove('task-item-done');
    taskItem.classList.add('task-item-active');
    const doneBox = taskItem.querySelector('.task-checkbox-done');
    if (doneBox) {
      doneBox.className = 'task-checkbox-ring';
      doneBox.setAttribute('aria-hidden', 'true');
      doneBox.innerHTML = '';
    }
  }

  /**
   * Re-apply optimistic styling to any tasks currently in-flight across SSE DOM updates.
   */
  function syncInFlightTasks(container) {
    const root = container || (typeof document !== 'undefined' ? document : null);
    if (!root || inFlightCompletions.size === 0) return;
    const items = root.querySelectorAll('.task-item');
    items.forEach((item) => {
      const tid = item.dataset.taskId;
      if (tid && inFlightCompletions.has(tid)) {
        item.dataset.completing = 'true';
        applyTaskDoneState(item);
      }
    });
  }

  const calendarEventStore = new Map();
  let headerNextEventInitialized = false;

  /**
   * Format relative countdown string (e.g. "in 1h 30m", "in 45m", "ends in 15m").
   */
  function formatRelativeCountdown(ms, isEnding = false) {
    const totalMinutes = Math.round(ms / 60000);
    const prefix = isEnding ? 'ends in ' : 'in ';
    if (totalMinutes <= 0) return `${prefix}<1m`;
    if (totalMinutes < 60) return `${prefix}${totalMinutes}m`;
    const hours = Math.floor(totalMinutes / 60);
    const mins = totalMinutes % 60;
    if (mins === 0) return `${prefix}${hours}h`;
    return `${prefix}${hours}h ${mins}m`;
  }

  /**
   * Resolve timezone offset string for a given date in IANA timezone.
   */
  function getTimezoneOffsetString(date, tz) {
    if (!isValidTimezone(tz)) return '';
    try {
      const formatter = new Intl.DateTimeFormat('en-US', {
        timeZone: tz,
        timeZoneName: 'longOffset',
      });
      const parts = formatter.formatToParts(date);
      const tzPart = parts.find((p) => p.type === 'timeZoneName');
      if (tzPart && tzPart.value) {
        const m = tzPart.value.match(/GMT([+-]\d{2}:\d{2})/);
        if (m) return m[1];
        if (tzPart.value === 'GMT' || tzPart.value === 'UTC') return 'Z';
      }
    } catch {}
    return '';
  }

  /**
   * Resolve year, month, day components of a Date in the given timezone.
   */
  function getDatePartsInTimezone(date, tz) {
    if (!isValidTimezone(tz)) {
      return {
        year: date.getFullYear(),
        month: date.getMonth() + 1,
        day: date.getDate(),
      };
    }
    try {
      const formatter = new Intl.DateTimeFormat('en-US', {
        timeZone: tz,
        year: 'numeric',
        month: 'numeric',
        day: 'numeric',
      });
      const parts = formatter.formatToParts(date);
      let year = date.getFullYear();
      let month = date.getMonth() + 1;
      let day = date.getDate();
      for (const p of parts) {
        if (p.type === 'year') year = parseInt(p.value, 10);
        if (p.type === 'month') month = parseInt(p.value, 10);
        if (p.type === 'day') day = parseInt(p.value, 10);
      }
      return { year, month, day };
    } catch {
      return {
        year: date.getFullYear(),
        month: date.getMonth() + 1,
        day: date.getDate(),
      };
    }
  }

  /**
   * Parse raw calendar event dates into normalized Date objects.
   */
  function parseEventDates(raw, referenceNow, tz) {
    if (!raw || typeof raw !== 'object') return null;
    const offsetStr = getTimezoneOffsetString(referenceNow, tz);
    const isAllDay = Boolean(
      raw.allDay ||
      raw.all_day ||
      (typeof raw.start === 'string' && raw.start.length === 10 && !raw.start.includes(':'))
    );
    let startDate = null;
    let endDate = null;

    if (isAllDay) {
      let dateStr = typeof raw.start === 'string' ? raw.start.substring(0, 10) : '';
      if (!dateStr && raw.date) dateStr = raw.date;
      if (!dateStr && raw.dayDate) dateStr = raw.dayDate;
      if (dateStr && dateStr.match(/^\d{4}-\d{2}-\d{2}$/)) {
        startDate = new Date(`${dateStr}T00:00:00${offsetStr}`);
        let endDateStr = typeof raw.end === 'string' ? raw.end.substring(0, 10) : dateStr;
        if (!endDateStr || !endDateStr.match(/^\d{4}-\d{2}-\d{2}$/)) endDateStr = dateStr;
        endDate = new Date(`${endDateStr}T23:59:59.999${offsetStr}`);
      } else if (raw.start instanceof Date) {
        startDate = new Date(raw.start);
        endDate = raw.end instanceof Date ? new Date(raw.end) : new Date(startDate.getTime() + 24 * 3600 * 1000 - 1);
      }
    } else {
      if (typeof raw.start === 'string' && raw.start.match(/^\d{1,2}:\d{2}$/)) {
        let dateStr = raw.date || raw.dayDate;
        if (!dateStr || !dateStr.match(/^\d{4}-\d{2}-\d{2}$/)) {
          const np = getDatePartsInTimezone(referenceNow, tz);
          dateStr = `${np.year}-${String(np.month).padStart(2, '0')}-${String(np.day).padStart(2, '0')}`;
        }
        const [sh, sm] = raw.start.split(':').map((s) => s.padStart(2, '0'));
        startDate = new Date(`${dateStr}T${sh}:${sm}:00${offsetStr}`);

        if (typeof raw.end === 'string' && raw.end.match(/^\d{1,2}:\d{2}$/)) {
          const [eh, em] = raw.end.split(':').map((s) => s.padStart(2, '0'));
          endDate = new Date(`${dateStr}T${eh}:${em}:00${offsetStr}`);
        } else {
          endDate = new Date(startDate.getTime() + 60 * 60 * 1000);
        }
      } else if (typeof raw.start === 'string') {
        startDate = new Date(raw.start);
        if (typeof raw.end === 'string') {
          endDate = new Date(raw.end);
        } else {
          endDate = new Date(startDate.getTime() + 60 * 60 * 1000);
        }
      } else if (raw.start instanceof Date) {
        startDate = new Date(raw.start);
        endDate = raw.end instanceof Date ? new Date(raw.end) : new Date(startDate.getTime() + 60 * 60 * 1000);
      }
    }

    if (!startDate || isNaN(startDate.getTime()) || !endDate || isNaN(endDate.getTime())) {
      return null;
    }

    return {
      raw,
      id: raw.id || `${raw.title}-${raw.start}`,
      title: raw.title || 'Event',
      startDate,
      endDate,
      isAllDay,
    };
  }

  /**
   * Resolve earliest next up or ongoing event across candidates.
   */
  function resolveNextUpEvent(events, now, tz) {
    if (!Array.isArray(events) || events.length === 0) return null;
    const activeTz = tz || getTimezone();

    const normalized = [];
    for (const raw of events) {
      const parsed = parseEventDates(raw, now, activeTz);
      if (!parsed) continue;
      if (parsed.endDate <= now) continue;
      normalized.push(parsed);
    }

    if (normalized.length === 0) return null;

    const ongoingTimed = [];
    const upcomingTimed = [];
    const allDayToday = [];
    const upcomingAllDay = [];

    for (const ev of normalized) {
      if (ev.isAllDay) {
        if (ev.startDate <= now && ev.endDate >= now) {
          allDayToday.push(ev);
        } else if (ev.startDate > now) {
          upcomingAllDay.push(ev);
        }
      } else {
        if (ev.startDate <= now && ev.endDate > now) {
          ongoingTimed.push(ev);
        } else if (ev.startDate > now) {
          upcomingTimed.push(ev);
        }
      }
    }

    ongoingTimed.sort((a, b) => a.endDate.getTime() - b.endDate.getTime());
    upcomingTimed.sort((a, b) => a.startDate.getTime() - b.startDate.getTime());
    allDayToday.sort((a, b) => a.startDate.getTime() - b.startDate.getTime());
    upcomingAllDay.sort((a, b) => a.startDate.getTime() - b.startDate.getTime());

    // 1. Ongoing timed event takes top priority
    if (ongoingTimed.length > 0) {
      const ev = ongoingTimed[0];
      const remainingMs = ev.endDate.getTime() - now.getTime();
      return {
        event: ev.raw,
        title: ev.title,
        label: 'NOW',
        timeStr: formatTime(ev.startDate, activeTz),
        countdown: formatRelativeCountdown(remainingMs, true),
        isNow: true,
      };
    }

    // 2. Upcoming timed event today takes precedence over all-day event
    const nowParts = getDatePartsInTimezone(now, activeTz);
    const todayUpcoming = upcomingTimed.filter((e) => {
      const ep = getDatePartsInTimezone(e.startDate, activeTz);
      return ep.year === nowParts.year && ep.month === nowParts.month && ep.day === nowParts.day;
    });

    if (todayUpcoming.length > 0) {
      const ev = todayUpcoming[0];
      const untilMs = ev.startDate.getTime() - now.getTime();
      return {
        event: ev.raw,
        title: ev.title,
        label: 'NEXT',
        timeStr: formatTime(ev.startDate, activeTz),
        countdown: formatRelativeCountdown(untilMs, false),
        isNow: false,
      };
    }

    // 3. All-day event today
    if (allDayToday.length > 0) {
      const ev = allDayToday[0];
      return {
        event: ev.raw,
        title: ev.title,
        label: 'TODAY',
        timeStr: 'All Day',
        countdown: '',
        isNow: false,
      };
    }

    // 4. Upcoming timed event on a future day
    if (upcomingTimed.length > 0) {
      const ev = upcomingTimed[0];
      const untilMs = ev.startDate.getTime() - now.getTime();
      const dayOpts = { weekday: 'short' };
      if (isValidTimezone(activeTz)) dayOpts.timeZone = activeTz;
      const weekday = new Intl.DateTimeFormat(undefined, dayOpts).format(ev.startDate);
      return {
        event: ev.raw,
        title: ev.title,
        label: 'NEXT',
        timeStr: `${weekday} ${formatTime(ev.startDate, activeTz)}`,
        countdown: formatRelativeCountdown(untilMs, false),
        isNow: false,
      };
    }

    // 5. Upcoming all-day event on a future day
    if (upcomingAllDay.length > 0) {
      const ev = upcomingAllDay[0];
      const dayOpts = { weekday: 'short' };
      if (isValidTimezone(activeTz)) dayOpts.timeZone = activeTz;
      const weekday = new Intl.DateTimeFormat(undefined, dayOpts).format(ev.startDate);
      return {
        event: ev.raw,
        title: ev.title,
        label: weekday.toUpperCase(),
        timeStr: 'All Day',
        countdown: '',
        isNow: false,
      };
    }

    return null;
  }

  /**
   * Scan DOM for calendar-feed and calendar-grid data and ingest into calendarEventStore.
   */
  function scanAndIngestCalendarWidgets(root) {
    const doc = root || (typeof document !== 'undefined' ? document : null);
    if (!doc || typeof doc.querySelectorAll !== 'function') return;

    // Ingest calendar-feed data
    const feedScripts = doc.querySelectorAll('.calendar-feed-data');
    if (feedScripts && feedScripts.length > 0) {
      feedScripts.forEach((script, idx) => {
        try {
          const data = JSON.parse(script.textContent || '[]');
          if (Array.isArray(data)) {
            const events = data.map((item, i) => ({
              id: item.id || `feed-${idx}-${i}`,
              title: item.title,
              start: item.start,
              end: item.end,
              allDay: Boolean(item.all_day || item.allDay),
              location: item.location,
            }));
            calendarEventStore.set(`feed-${idx}`, events);
          }
        } catch {}
      });
    }

    // Fallback: ingest calendar-feed DOM elements
    const feedItems = doc.querySelectorAll('.calendar-event-item');
    if (feedItems && feedItems.length > 0 && (!feedScripts || feedScripts.length === 0)) {
      const events = [];
      feedItems.forEach((el, idx) => {
        const id = el.dataset?.eventId || el.getAttribute?.('data-event-id') || `feed-item-${idx}`;
        const title = el.dataset?.eventTitle || el.getAttribute?.('data-event-title') || el.querySelector?.('.event-title')?.textContent?.trim() || el.textContent.trim();
        const start = el.dataset?.eventStart || el.getAttribute?.('data-event-start');
        const end = el.dataset?.eventEnd || el.getAttribute?.('data-event-end');
        const allDay = el.dataset?.eventAllday === 'true' || el.getAttribute?.('data-event-allday') === 'true';
        if (title && (start || allDay)) {
          events.push({ id, title, start, end, allDay });
        }
      });
      if (events.length > 0) {
        calendarEventStore.set('feed-dom', events);
      }
    }

    // Ingest calendar-grid data
    const gridScripts = doc.querySelectorAll('.calendar-grid-data, .calendar-family-data');
    if (gridScripts && gridScripts.length > 0) {
      gridScripts.forEach((script, idx) => {
        try {
          const data = JSON.parse(script.textContent || '{}');
          const days = Array.isArray(data.days) ? data.days : Array.isArray(data) ? data : [];
          const events = [];
          const pushGridEvent = (ev, dayDate, forceAllDay = false) => {
            if (!ev || !ev.title) return;
            const isAllDay = Boolean(forceAllDay || ev.all_day || ev.allDay || !ev.start);
            events.push({
              id: ev.id ? `${ev.id}-${dayDate}` : `grid-${dayDate}-${ev.start || 'allday'}-${ev.title}`,
              title: ev.title,
              start: ev.start || dayDate,
              end: ev.end || dayDate,
              allDay: isAllDay,
              date: dayDate,
              dayDate: dayDate,
            });
          };
          for (const day of days) {
            const dayDate = day.date;
            for (const group of [day.events, day.early_events, day.late_events]) {
              if (Array.isArray(group)) {
                for (const ev of group) pushGridEvent(ev, dayDate, false);
              }
            }
            if (Array.isArray(day.all_day)) {
              for (const ev of day.all_day) pushGridEvent(ev, dayDate, true);
            }
          }
          if (events.length > 0) {
            calendarEventStore.set(`grid-${idx}`, events);
          }
        } catch {}
      });
    }

    // Fallback: ingest calendar-grid DOM elements
    const gridDays = doc.querySelectorAll('.cf-day');
    if (gridDays && gridDays.length > 0 && (!gridScripts || gridScripts.length === 0)) {
      const events = [];
      gridDays.forEach((dayEl, dIdx) => {
        const dayDate = dayEl.dataset?.date || dayEl.getAttribute?.('data-date');
        const dayEvents = dayEl.querySelectorAll?.('.cf-day-event, .cf-allday, .cf-edge-event') || [];
        dayEvents.forEach((evEl, eIdx) => {
          const id = evEl.dataset?.eventId || evEl.getAttribute?.('data-event-id') || `grid-dom-${dIdx}-${eIdx}`;
          const title = evEl.dataset?.eventTitle || evEl.getAttribute?.('data-event-title') || evEl.textContent.trim();
          const start = evEl.dataset?.start || evEl.getAttribute?.('data-start');
          const end = evEl.dataset?.end || evEl.getAttribute?.('data-end');
          const allDay = evEl.classList?.contains?.('cf-allday') || evEl.dataset?.allday === 'true';
          if (title) {
            events.push({ id, title, start, end, allDay, date: dayDate, dayDate });
          }
        });
      });
      if (events.length > 0) {
        calendarEventStore.set('grid-dom', events);
      }
    }
  }

  /**
   * Get all cached calendar events across all ingested widgets.
   */
  function getAllStoredCalendarEvents() {
    const deduped = new Map();
    for (const events of calendarEventStore.values()) {
      if (!Array.isArray(events)) continue;
      for (const ev of events) {
        if (!ev) continue;
        const key = ev.id || `${ev.title}-${ev.start}`;
        if (!deduped.has(key)) {
          deduped.set(key, ev);
        }
      }
    }
    return Array.from(deduped.values());
  }

  /**
   * Update header next up event pill in DOM.
   */
  function updateNextUpEvent(customInstant, explicitEvents) {
    if (typeof document === 'undefined') return;
    const now = customInstant instanceof Date ? customInstant : new Date();
    const tz = getTimezone();

    let events = explicitEvents;
    if (!events) {
      let stored = getAllStoredCalendarEvents();
      if (!stored || stored.length === 0) {
        scanAndIngestCalendarWidgets();
        stored = getAllStoredCalendarEvents();
      }
      events = stored;
    }

    const container = document.getElementById('header-next-event');
    if (!container) return;

    if (!events || events.length === 0) {
      container.style.display = 'none';
      return;
    }

    const resolved = resolveNextUpEvent(events, now, tz);
    if (!resolved) {
      container.style.display = 'none';
      return;
    }

    container.style.display = '';

    const pillEl = document.getElementById('next-event-pill');
    const labelEl = document.getElementById('next-event-label');
    const timeEl = document.getElementById('next-event-time');
    const titleEl = document.getElementById('next-event-title');
    const countdownEl = document.getElementById('next-event-countdown');

    if (labelEl) labelEl.textContent = resolved.label;
    if (timeEl) timeEl.textContent = resolved.timeStr;
    if (titleEl) {
      titleEl.textContent = resolved.title;
      titleEl.title = resolved.title;
    }
    if (countdownEl) countdownEl.textContent = resolved.countdown;

    if (pillEl) {
      if (resolved.isNow) {
        pillEl.classList.add('status-now');
      } else {
        pillEl.classList.remove('status-now');
      }
      pillEl.title = `${resolved.label}: ${resolved.title} (${resolved.timeStr})`;
    }
  }

  /**
   * Setup header next up event pill interaction and accessibility.
   */
  function setupHeaderNextEvent() {
    if (typeof document === 'undefined' || headerNextEventInitialized) return;
    headerNextEventInitialized = true;

    const pillEl = document.getElementById('next-event-pill');
    if (!pillEl) return;

    pillEl.addEventListener('click', () => {
      const target = document.querySelector('.widget-calendar-feed, .widget-calendar-grid, .calendar-event-item, .cf-day');
      if (target && typeof target.scrollIntoView === 'function') {
        target.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      }
    });

    pillEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        pillEl.click();
      }
    });
  }


  /**
   * Complete task action handler with optimistic UI updates and network rollback.
   */
  async function completeTask(taskItem) {
    if (!taskItem) return;
    if (taskItem.classList.contains('task-item-done') || taskItem.dataset.completing === 'true') {
      return;
    }

    const taskId = taskItem.dataset.taskId;
    const listId = taskItem.dataset.listId || taskItem.closest('.widget-tasks')?.dataset?.widgetId;
    if (!taskId || !listId) return;

    taskItem.dataset.completing = 'true';
    inFlightCompletions.add(taskId);
    applyTaskDoneState(taskItem);

    try {
      const resp = await fetch(`api/lists/${encodeURIComponent(listId)}/items/${encodeURIComponent(taskId)}/complete`, {
        method: 'POST',
        headers: {
          'Accept': 'application/json',
        },
      });
      if (!resp.ok) {
        throw new Error(`HTTP ${resp.status}`);
      }
    } catch (err) {
      inFlightCompletions.delete(taskId);
      delete taskItem.dataset.completing;
      revertTaskDoneState(taskItem);
      if (typeof console !== 'undefined' && console.error) {
        console.error('Failed to complete task:', err);
      }
      throw err;
    } finally {
      inFlightCompletions.delete(taskId);
      delete taskItem.dataset.completing;
    }
  }

  let tasksInteractionInitialized = false;

  /**
   * Initialize delegated touch and click handlers for tasks widget completion.
   */
  function initTasksInteraction() {
    if (typeof document === 'undefined' || tasksInteractionInitialized) return;
    tasksInteractionInitialized = true;

    document.addEventListener('click', (e) => {
      const taskItem = e.target.closest('.task-item');
      if (taskItem) {
        completeTask(taskItem).catch(() => {});
      }
    });
  }

  /**
   * Initialize display client application.
   */
  function init() {
    if (typeof document === 'undefined') return;

    // 0. Resolve cursor suppression query overrides & theme toggle
    resolveCursorSuppression();
    setupThemeToggle();

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
        const navBarEl = document.getElementById('video-hud-nav-bar');
        const backBtn = document.getElementById('video-hud-back-btn');
        const homeBtn = document.getElementById('video-hud-home-btn');
        const ccBtn = document.getElementById('video-hud-cc-btn');
        const prevBtn = document.getElementById('video-hud-prev-btn');
        const nextBtn = document.getElementById('video-hud-next-btn');
        window.videoHUD = new window.MirrormereHUD.VideoHUDController({
          overlayElement: overlayEl,
          stageElement: stageEl,
          bottomBarElement: bottomBarEl,
          rewindBtn: rewindBtn,
          forwardBtn: forwardBtn,
          playBtn: playBtn,
          navBarElement: navBarEl,
          backBtn: backBtn,
          homeBtn: homeBtn,
          ccBtn: ccBtn,
          prevBtn: prevBtn,
          nextBtn: nextBtn,
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

      // 7a. Initialize Voice Waveform Mouth (fed by the ear daemon's /levels stream)
      if (window.MirrormereWaveform && window.MirrormereWaveform.WaveformMouth) {
        const body = document.body;
        const url = body && body.dataset ? body.dataset.voiceWaveformUrl : '';
        if (url) {
          window.voiceWaveform = new window.MirrormereWaveform.WaveformMouth({
            sseClient: sseClient,
            levelsUrl: url,
            gain: parseFloat(body.dataset.voiceWaveformGain) || undefined,
          });
        }
      }

      // 7b. Initialize Tasks Widget Interaction
      initTasksInteraction();

      // 8. Initialize Header Video Action Button
      setupHeaderVideo({
        sseClient: sseClient,
      });

      // 8b. Initialize Header Next Up Event Pill
      setupHeaderNextEvent();
      scanAndIngestCalendarWidgets();
      updateNextUpEvent();

      sseClient.on('screen.rotate', async (data) => {
        if (carousel) await carousel.handleScreenRotate(data);
        scanAndIngestCalendarWidgets();
        updateNextUpEvent();
      });

      sseClient.on('widget.update', async (data) => {
        if (carousel) await carousel.handleWidgetUpdate(data);
        syncInFlightTasks();
        scanAndIngestCalendarWidgets();
        updateNextUpEvent();
      });

      sseClient.on('widget.reload', async (data) => {
        if (carousel) await carousel.handleWidgetReload(data);
        syncInFlightTasks();
        scanAndIngestCalendarWidgets();
        updateNextUpEvent();
      });

      sseClient.on('style.reload', (data) => {
        handleStyleReload(data);
        checkStyleHeartbeat();
      });

      sseClient.on('connection', (data) => {
        if (data && data.status === 'connected') {
          handleSystemStatus({ online: true, config_status: lastConfigStatus, config_error: lastConfigError });
          checkStyleHeartbeat();
        } else if (data && data.status === 'disconnected') {
          handleSystemStatus({ online: false });
        }
      });

      sseClient.on('header.update', (data) => {
        handleHeaderUpdate(data);
      });

      sseClient.on('theme.update', (data) => {
        handleThemeUpdate(data);
      });

      sseClient.on('system.status', (data) => {
        handleSystemStatus(data);
      });

      sseClient.on('system.reload', (data) => {
        handleSystemReload(data);
      });

      sseClient.connect();
    } else {
      // Standalone or pre-SSE header video & tasks initialization
      setupHeaderVideo();
      initTasksInteraction();
      setupHeaderNextEvent();
      scanAndIngestCalendarWidgets();
      updateNextUpEvent();
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
    handleThemeUpdate,
    getTheme,
    setTheme,
    toggleTheme,
    resetThemeToAuto,
    setupThemeToggle,
    handleSystemStatus,
    handleSystemReload,
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
    completeTask,
    initTasksInteraction,
    syncInFlightTasks,
    getInFlightCompletions: () => inFlightCompletions,
    updateNextUpEvent,
    resolveNextUpEvent,
    scanAndIngestCalendarWidgets,
    getAllStoredCalendarEvents,
    calendarEventStore,
    clearCalendarEventStore: () => calendarEventStore.clear(),
    formatRelativeCountdown,
    setupHeaderNextEvent,
    init,
  };

  if (typeof window !== 'undefined') {
    window.MirrormereDisplay = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
})();
