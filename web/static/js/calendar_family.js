/**
 * Mirrormere Calendar Family Widget Controller
 * Manages 3-way view switching (Day | Week | Month), active view state synchronization,
 * tap-to-expand event detail modal, carousel rotation pausing, and inactivity reset timer.
 */
(() => {
  class CalendarFamilyInstance {
    constructor(element, options = {}) {
      this.element = element;
      this.options = options;
      this.widgetId = element.dataset.widgetId || 'calendar-family';
      this.data = this.parseData();
      this.config = this.parseConfig();
      this.eventIndex = this.buildEventIndex();
      this.defaultView = this.resolveDefaultView();
      this.activeView = this.resolveActiveView();
      this.activeModal = null;
      this.inactivityTimeout = typeof options.inactivityTimeout === 'number' ? options.inactivityTimeout : 60000;
      this.inactivityTimer = null;

      this.viewButtons = Array.from(element.querySelectorAll('.cf-view-btn'));
      this.viewPanels = {
        day: element.querySelector('.cf-view-panel.cf-view-day') || element.querySelector('.cf-view-day'),
        week: element.querySelector('.cf-view-panel.cf-view-week') || element.querySelector('.cf-view-week'),
        month: element.querySelector('.cf-view-panel.cf-view-month') || element.querySelector('.cf-view-month'),
      };
      this.labels = {
        day: element.querySelector('.cf-range-label.cf-label-day'),
        week: element.querySelector('.cf-range-label.cf-label-week'),
        month: element.querySelector('.cf-range-label.cf-label-month'),
      };
      this.todayBadge = element.querySelector('.cf-today-badge.cf-today-day') || element.querySelector('.cf-today-badge');

      this.clickHandler = this.handleClick.bind(this);
      this.activityHandler = this.handleUserActivity.bind(this);
      this.keydownHandler = this.handleKeyDown.bind(this);
      this.init();
    }

    parseData() {
      const dataEl = this.element.querySelector('.calendar-family-data');
      if (dataEl && dataEl.textContent) {
        try {
          let parsed = JSON.parse(dataEl.textContent.trim());
          if (typeof parsed === 'string') {
            try {
              parsed = JSON.parse(parsed);
            } catch (_) {}
          }
          return parsed || {};
        } catch (e) {
          console.warn(`[MirrormereCalendarFamily] Failed to parse calendar data for ${this.widgetId}:`, e);
        }
      }
      return {};
    }

    parseConfig() {
      if (this.data && this.data.config) {
        return this.data.config;
      }
      return {};
    }

    buildEventIndex() {
      const index = new Map();
      const addEv = (ev) => {
        if (!ev) return;
        if (ev.id) index.set(String(ev.id), ev);
        if (ev.title && !index.has(String(ev.title))) {
          index.set(String(ev.title), ev);
        }
      };

      // 1. Day view:
      if (Array.isArray(this.data.all_day_events)) {
        for (const ev of this.data.all_day_events) addEv(ev);
      }
      if (Array.isArray(this.data.columns)) {
        for (const col of this.data.columns) {
          if (Array.isArray(col.events)) for (const ev of col.events) addEv(ev);
          if (Array.isArray(col.early_events)) for (const ev of col.early_events) addEv(ev);
          if (Array.isArray(col.late_events)) for (const ev of col.late_events) addEv(ev);
        }
      }

      // 2. Week view / Month view:
      if (Array.isArray(this.data.days)) {
        for (const day of this.data.days) {
          if (Array.isArray(day.all_day)) for (const ev of day.all_day) addEv(ev);
          if (Array.isArray(day.timed)) for (const ev of day.timed) addEv(ev);
          if (Array.isArray(day.events)) for (const ev of day.events) addEv(ev);
        }
      }

      return index;
    }

    resolveDefaultView() {
      const raw = (this.element.dataset.defaultView || (this.config && this.config.default_view) || 'week').toLowerCase();
      if (raw === 'day' || raw === 'month') {
        return raw;
      }
      return 'week';
    }

    resolveActiveView() {
      const raw = (this.element.dataset.activeView || this.defaultView).toLowerCase();
      if (raw === 'day' || raw === 'month') {
        return raw;
      }
      return 'week';
    }

    init() {
      this.bindViewSwitcher();
      this.bindInteractions();
      this.updateViewUI(this.activeView);
      this.resetInactivityTimer();
    }

    bindViewSwitcher() {
      for (const btn of this.viewButtons) {
        btn.addEventListener('click', this.clickHandler);
      }
    }

    bindInteractions() {
      this.element.addEventListener('click', this.clickHandler);
      const activityEvents = ['touchstart', 'touchmove', 'touchend', 'pointerdown', 'mousedown'];
      for (const evt of activityEvents) {
        this.element.addEventListener(evt, this.activityHandler, { passive: true });
      }
      if (typeof document !== 'undefined' && document.addEventListener) {
        document.addEventListener('keydown', this.keydownHandler);
      }
    }

    handleUserActivity() {
      this.resetInactivityTimer();
    }

    resetInactivityTimer() {
      if (this.inactivityTimer) {
        clearTimeout(this.inactivityTimer);
      }
      this.inactivityTimer = setTimeout(() => {
        this.handleInactivityTimeout();
      }, this.inactivityTimeout);
      if (this.inactivityTimer && typeof this.inactivityTimer.unref === 'function') {
        this.inactivityTimer.unref();
      }
    }

    handleInactivityTimeout() {
      this.inactivityTimer = null;

      // 1. If detail modal is open, dismiss it
      if (this.activeModal) {
        this.closeModal();
      }

      // 2. Return active view to defaultView
      if (this.activeView !== this.defaultView) {
        this.switchView(this.defaultView);
      }

      if (typeof this.options.onInactivityReset === 'function') {
        this.options.onInactivityReset(this);
      }
    }

    handleKeyDown(e) {
      if (e.key === 'Escape' && this.activeModal) {
        this.closeModal();
      }
    }

    handleClick(e) {
      this.resetInactivityTimer();

      // 1. Check view switcher button click
      const btn = e.target && e.target.closest && e.target.closest('.cf-view-btn');
      if (btn && this.viewButtons.includes(btn)) {
        const targetView = (btn.dataset.view || '').toLowerCase();
        if (targetView && (targetView === 'day' || targetView === 'week' || targetView === 'month')) {
          this.switchView(targetView);
        }
        return;
      }

      // 2. Check modal backdrop or close button click
      if (this.activeModal) {
        const closeBtn = e.target && e.target.closest && e.target.closest('.cf-modal-close');
        const overlay = e.target && e.target.classList && e.target.classList.contains('cf-modal-overlay');
        if (closeBtn || overlay) {
          this.closeModal();
          return;
        }
        const modalCard = e.target && e.target.closest && e.target.closest('.cf-modal-card');
        if (modalCard) {
          return;
        }
      }

      // 3. Check event element click/tap
      const eventEl = e.target && e.target.closest && e.target.closest('.cf-event-clickable, .cf-event, .cf-day-event, .cf-edge-event, .cf-month-event-title, .cf-has-events, [data-event-id]');
      if (eventEl && this.element.contains(eventEl)) {
        const evData = this.resolveEventData(eventEl);
        if (evData) {
          if (typeof e.preventDefault === 'function') {
            e.preventDefault();
          }
          this.openModal(evData);
        }
      }
    }

    resolveEventData(eventEl) {
      if (!eventEl) return null;

      // 1. Search eventIndex by data-event-id
      const id = eventEl.dataset && eventEl.dataset.eventId;
      if (id && this.eventIndex.has(id)) {
        return this.eventIndex.get(id);
      }

      // 2. Search eventIndex by title
      const titleAttr = (eventEl.dataset && (eventEl.dataset.eventTitle || eventEl.dataset.title)) || (eventEl.getAttribute && eventEl.getAttribute('title'));
      if (titleAttr && this.eventIndex.has(titleAttr)) {
        return this.eventIndex.get(titleAttr);
      }

      // 3. Check Month view day cell with events
      if (eventEl.dataset && eventEl.dataset.date && Array.isArray(this.data.days)) {
        const day = this.data.days.find(d => d.date === eventEl.dataset.date);
        if (day && Array.isArray(day.events) && day.events.length > 0) {
          return day.events[0];
        }
      }

      // 4. Construct from DOM dataset attributes
      if (eventEl.dataset && (eventEl.dataset.eventTitle || eventEl.dataset.title || eventEl.textContent)) {
        const title = eventEl.dataset.eventTitle || eventEl.dataset.title || (eventEl.querySelector && eventEl.querySelector('.cf-event-title')?.textContent) || eventEl.textContent?.trim();
        const start = eventEl.dataset.eventStart || eventEl.dataset.start || (eventEl.querySelector && eventEl.querySelector('.cf-event-time')?.textContent) || '';
        const end = eventEl.dataset.eventEnd || eventEl.dataset.end || '';
        const allDay = eventEl.dataset.eventAllday === 'true' || eventEl.dataset.allDay === 'true' || (eventEl.classList && eventEl.classList.contains('cf-allday')) || false;
        const location = eventEl.dataset.eventLocation || eventEl.dataset.location || (eventEl.querySelector && eventEl.querySelector('.cf-event-location')?.textContent) || '';
        const description = eventEl.dataset.eventDescription || eventEl.dataset.description || '';
        const color = eventEl.dataset.color || (eventEl.style && eventEl.style.backgroundColor) || 'var(--mm-accent-purple)';
        const pattern = eventEl.dataset.pattern || 'solid';

        return {
          id: id || 'ev-' + Math.random().toString(36).slice(2, 8),
          title: title || 'Untitled Event',
          start,
          end,
          all_day: allDay,
          location,
          description,
          color,
          pattern,
          owners: [],
          initials: [],
        };
      }

      return null;
    }

    openModal(eventData) {
      if (!eventData) return;

      // Close previous modal without resume
      if (this.activeModal) {
        this.closeModal(true);
      }

      const ev = typeof eventData === 'object' ? eventData : { title: String(eventData) };

      // 1. Overlay
      const overlay = document.createElement('div');
      overlay.className = 'cf-modal-overlay';
      overlay.setAttribute('role', 'dialog');
      overlay.setAttribute('aria-modal', 'true');
      overlay.setAttribute('aria-label', ev.title || 'Event Details');

      // 2. Modal card
      const card = document.createElement('div');
      card.className = 'cf-modal-card';

      // 3. Header: Title + Close button
      const header = document.createElement('div');
      header.className = 'cf-modal-header';

      const titleEl = document.createElement('h3');
      titleEl.className = 'cf-modal-title';
      titleEl.textContent = ev.title || 'Untitled Event';
      header.appendChild(titleEl);

      const closeBtn = document.createElement('button');
      closeBtn.type = 'button';
      closeBtn.className = 'cf-modal-close';
      closeBtn.setAttribute('aria-label', 'Close');
      closeBtn.innerHTML = '&times;';
      closeBtn.addEventListener('click', (e) => {
        if (typeof e.stopPropagation === 'function') {
          e.stopPropagation();
        }
        this.closeModal();
      });
      header.appendChild(closeBtn);
      card.appendChild(header);

      // 4. Formatted Time
      let timeText = '';
      if (ev.all_day || ev.allDay || ev.is_all_day) {
        timeText = 'All Day';
      } else if (ev.start && ev.end) {
        timeText = `${ev.start} – ${ev.end}`;
      } else if (ev.start) {
        timeText = ev.start;
      }
      if (timeText) {
        const timeEl = document.createElement('div');
        timeEl.className = 'cf-modal-time';
        timeEl.textContent = `🕒 ${timeText}`;
        card.appendChild(timeEl);
      }

      // 5. Location
      if (ev.location && ev.location.trim()) {
        const locEl = document.createElement('div');
        locEl.className = 'cf-modal-location';
        locEl.textContent = `📍 ${ev.location.trim()}`;
        card.appendChild(locEl);
      }

      // 6. Owner chips / initials
      const owners = ev.owners || [];
      const initials = ev.initials || [];
      const colors = ev.colors || (ev.color ? [ev.color] : []);
      const patterns = ev.patterns || (ev.pattern ? [ev.pattern] : ['solid']);

      if (owners.length > 0 || initials.length > 0) {
        const ownersContainer = document.createElement('div');
        ownersContainer.className = 'cf-modal-owners';

        const count = Math.max(owners.length, initials.length);
        for (let i = 0; i < count; i++) {
          const ownerName = owners[i] || '';
          const initial = initials[i] || (ownerName ? ownerName.charAt(0).toUpperCase() : '');
          const color = colors[i] || ev.color || 'var(--mm-accent-purple)';
          const pattern = patterns[i] || ev.pattern || 'solid';

          const chip = document.createElement('span');
          chip.className = `cf-modal-owner-chip cf-pattern-${pattern}`;

          const badge = document.createElement('span');
          badge.className = 'cf-modal-owner-badge';
          badge.style.backgroundColor = color;
          badge.textContent = initial;
          chip.appendChild(badge);

          if (ownerName) {
            const nameSpan = document.createElement('span');
            nameSpan.className = 'cf-modal-owner-name';
            nameSpan.textContent = ownerName;
            chip.appendChild(nameSpan);
          }
          ownersContainer.appendChild(chip);
        }
        card.appendChild(ownersContainer);
      }

      // 7. Event Description (up to 280 characters)
      if (ev.description && ev.description.trim()) {
        let desc = ev.description.trim();
        if (desc.length > 280) {
          desc = desc.slice(0, 277) + '...';
        }
        const descEl = document.createElement('div');
        descEl.className = 'cf-modal-description';
        descEl.textContent = desc;
        card.appendChild(descEl);
      }

      overlay.appendChild(card);

      // Backdrop dismiss
      overlay.addEventListener('click', (e) => {
        if (e.target === overlay) {
          this.closeModal();
        }
      });

      this.element.appendChild(overlay);
      this.activeModal = overlay;

      // Pause carousel rotation
      this.pauseCarousel();

      if (typeof this.options.onModalOpen === 'function') {
        this.options.onModalOpen(ev, this);
      }
    }

    closeModal(isReplacing = false) {
      if (!this.activeModal) return;

      const modalEl = this.activeModal;
      this.activeModal = null;
      if (modalEl.parentNode) {
        modalEl.parentNode.removeChild(modalEl);
      }

      if (!isReplacing) {
        this.resumeCarousel();
      }

      if (typeof this.options.onModalClose === 'function') {
        this.options.onModalClose(this);
      }
    }

    pauseCarousel() {
      if (typeof window !== 'undefined') {
        if (window.MirrormereCarousel && typeof window.MirrormereCarousel.pause === 'function') {
          try { window.MirrormereCarousel.pause(); } catch (_) {}
        }
        if (window.carousel && typeof window.carousel.pause === 'function') {
          try { window.carousel.pause(); } catch (_) {}
        }
      }
    }

    resumeCarousel() {
      if (typeof window !== 'undefined') {
        if (window.MirrormereCarousel && typeof window.MirrormereCarousel.resume === 'function') {
          try { window.MirrormereCarousel.resume(); } catch (_) {}
        }
        if (window.carousel && typeof window.carousel.resume === 'function') {
          try { window.carousel.resume(); } catch (_) {}
        }
      }
    }

    switchView(targetView) {
      const view = (targetView || '').toLowerCase();
      if (view !== 'day' && view !== 'week' && view !== 'month') {
        return;
      }
      this.activeView = view;
      this.element.dataset.activeView = view;
      this.updateViewUI(view);

      if (typeof this.options.onViewChange === 'function') {
        this.options.onViewChange(view, this);
      }
    }

    updateViewUI(view) {
      // 1. Update root container view classes
      this.element.classList.remove('cf-view-day');
      this.element.classList.remove('cf-view-week');
      this.element.classList.remove('cf-view-month');
      this.element.classList.add(`cf-view-${view}`);

      // 2. Update view switcher buttons
      for (const btn of this.viewButtons) {
        const isCurrent = btn.dataset.view === view;
        if (isCurrent) {
          btn.classList.add('active');
          btn.setAttribute('aria-selected', 'true');
        } else {
          btn.classList.remove('active');
          btn.setAttribute('aria-selected', 'false');
        }
      }

      // 3. Update view panels if multiple panels exist in DOM
      for (const [key, panel] of Object.entries(this.viewPanels)) {
        if (panel && panel !== this.element) {
          panel.style.display = (key === view ? 'flex' : 'none');
        }
      }

      // 4. Update header date range labels if specific label elements exist
      for (const [key, label] of Object.entries(this.labels)) {
        if (label) {
          label.style.display = (key === view ? '' : 'none');
        }
      }

      // 5. Update TODAY badge visibility (visible only in Day view)
      if (this.todayBadge) {
        this.todayBadge.style.display = (view === 'day' ? '' : 'none');
      }
    }

    destroy() {
      if (this.inactivityTimer) {
        clearTimeout(this.inactivityTimer);
        this.inactivityTimer = null;
      }
      for (const btn of this.viewButtons) {
        btn.removeEventListener('click', this.clickHandler);
      }
      this.element.removeEventListener('click', this.clickHandler);
      const activityEvents = ['touchstart', 'touchmove', 'touchend', 'pointerdown', 'mousedown'];
      for (const evt of activityEvents) {
        this.element.removeEventListener(evt, this.activityHandler);
      }
      if (typeof document !== 'undefined' && document.removeEventListener) {
        document.removeEventListener('keydown', this.keydownHandler);
      }
      if (this.activeModal) {
        this.closeModal();
      }
    }
  }

  // Global controller registration
  const api = {
    instances: new Map(),
    mount(element, options = {}) {
      if (!element) return null;
      const widgetId = element.dataset.widgetId || 'calendar-family';
      if (this.instances.has(widgetId)) {
        this.unmount(widgetId);
      }
      const instance = new CalendarFamilyInstance(element, options);
      this.instances.set(widgetId, instance);
      return instance;
    },
    unmount(widgetId) {
      const instance = this.instances.get(widgetId);
      if (instance) {
        instance.destroy();
        this.instances.delete(widgetId);
      }
    },
    getInstance(widgetId) {
      return this.instances.get(widgetId) || null;
    }
  };

  if (typeof window !== 'undefined') {
    window.MirrormereCalendarFamily = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = { CalendarFamilyInstance, MirrormereCalendarFamily: api };
  }
})();
