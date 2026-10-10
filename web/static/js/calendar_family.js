/**
 * Mirrormere Calendar Family Widget Controller
 * Manages 3-way view switching (Day | Week | Month), active view state synchronization,
 * tap-to-expand event detail modal, carousel rotation pausing, 60-second inactivity reset timer,
 * and horizontal touch swipe gesture navigation with window bounds paging.
 */
(() => {
  class CalendarFamilyInstance {
    constructor(element, options = {}) {
      this.element = element;
      this.options = options;
      this.widgetId = element.dataset.widgetId || element.dataset.widgetType || 'calendar-grid';
      this.data = this.parseData();
      this.config = this.parseConfig();
      this.eventIndex = this.buildEventIndex();
      this.defaultView = this.resolveDefaultView();
      this.activeView = this.resolveActiveView();
      this.activeModal = null;
      this.inactivityTimeout = typeof options.inactivityTimeout === 'number' ? options.inactivityTimeout : 60000;
      this.inactivityTimer = null;

      // Date state & window bounds
      this.baseDate = this.initBaseDate();
      this.currentDate = new Date(this.baseDate.getTime());
      this.baseWeekStart = this.initBaseWeekStart();
      this.currentWeekStart = new Date(this.baseWeekStart.getTime());
      this.pageOffset = 0;
      this.cachedRange = this.resolveCachedRange();
      this.syncIndicatorTimer = null;

      // Touch swipe configuration & state
      this.touchStartX = 0;
      this.touchStartY = 0;
      this.touchCurrentX = 0;
      this.touchCurrentY = 0;
      this.touchStartTime = 0;
      this.isSwiping = false;
      this.justSwiped = false;
      this.swipeDistanceThreshold = typeof options.swipeDistanceThreshold === 'number' ? options.swipeDistanceThreshold : 40;
      this.swipeVelocityThreshold = typeof options.swipeVelocityThreshold === 'number' ? options.swipeVelocityThreshold : 0.15;

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
      this.touchStartHandler = this.handleTouchStart.bind(this);
      this.touchMoveHandler = this.handleTouchMove.bind(this);
      this.touchEndHandler = this.handleTouchEnd.bind(this);

      this.init();
    }

    parseData() {
      const dataEl = this.element.querySelector('.calendar-grid-data, .calendar-family-data');
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
          if (Array.isArray(day.early_events)) for (const ev of day.early_events) addEv(ev);
          if (Array.isArray(day.late_events)) for (const ev of day.late_events) addEv(ev);
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

    initBaseDate() {
      let dateStr = (this.data && this.data.date) ||
                    (this.data && this.data.today) ||
                    (this.data && this.data.range_start) ||
                    (this.config && this.config.date);
      if (!dateStr && Array.isArray(this.data && this.data.days) && this.data.days.length > 0) {
        dateStr = this.data.days[0].date;
      }
      const parts = this.parseDateParts(dateStr);
      if (parts) {
        return this.createLocalDate(parts.year, parts.month, parts.day);
      }
      const now = new Date();
      return this.createLocalDate(now.getFullYear(), now.getMonth() + 1, now.getDate());
    }

    initBaseWeekStart() {
      let startStr = (this.data && this.data.range_start);
      if (!startStr && Array.isArray(this.data && this.data.days) && this.data.days.length > 0) {
        startStr = this.data.days[0].date;
      }
      const parts = this.parseDateParts(startStr);
      if (parts) {
        return this.createLocalDate(parts.year, parts.month, parts.day);
      }
      return new Date(this.baseDate.getTime());
    }

    resolveDaysCount() {
      if (this.config && typeof this.config.days === 'number' && this.config.days > 0) {
        return this.config.days;
      }
      if (this.config && typeof this.config.num_days === 'number' && this.config.num_days > 0) {
        return this.config.num_days;
      }
      return 7;
    }

    resolveCachedRange() {
      if (this.options.cachedRange) {
        return {
          start: typeof this.options.cachedRange.start === 'string'
            ? this.options.cachedRange.start
            : this.formatDateISO(this.options.cachedRange.start),
          end: typeof this.options.cachedRange.end === 'string'
            ? this.options.cachedRange.end
            : this.formatDateISO(this.options.cachedRange.end),
        };
      }
      let start = this.options.cachedRangeStart ||
                  (this.data && (this.data.cached_range_start || this.data.range_start)) ||
                  (this.config && this.config.cached_range_start);
      let end = this.options.cachedRangeEnd ||
                (this.data && (this.data.cached_range_end || this.data.range_end)) ||
                (this.config && this.config.cached_range_end);

      if (!start && Array.isArray(this.data && this.data.days) && this.data.days.length > 0) {
        start = this.data.days[0].date;
        end = this.data.days[this.data.days.length - 1].date;
      }
      if (!start && this.data && this.data.date) {
        start = this.data.date;
        end = this.data.date;
      }

      if (start && end) {
        return { start: String(start), end: String(end) };
      }
      return null;
    }

    getCachedDateRange() {
      return this.resolveCachedRange();
    }

    init() {
      this.bindViewSwitcher();
      this.bindInteractions();
      this.updateViewUI(this.activeView);
      this.updateDateUI();
      this.resetInactivityTimer();
    }

    bindViewSwitcher() {
      for (const btn of this.viewButtons) {
        btn.addEventListener('click', this.clickHandler);
      }
    }

    bindInteractions() {
      this.element.addEventListener('click', this.clickHandler);
      this.element.addEventListener('touchstart', this.touchStartHandler, { passive: true });
      this.element.addEventListener('touchmove', this.touchMoveHandler, { passive: true });
      this.element.addEventListener('touchend', this.touchEndHandler, { passive: true });

      const activityEvents = ['pointerdown', 'mousedown'];
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

      // 3. Reset date window to current/base date
      if (this.pageOffset !== 0) {
        this.resetDate();
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

    handleTouchStart(e) {
      this.resetInactivityTimer();
      if (this.activeModal) return;

      const touch = (e.touches && e.touches[0]) || (e.changedTouches && e.changedTouches[0]) || e;
      this.touchStartX = touch.clientX !== undefined ? touch.clientX : (touch.pageX || 0);
      this.touchStartY = touch.clientY !== undefined ? touch.clientY : (touch.pageY || 0);
      this.touchCurrentX = this.touchStartX;
      this.touchCurrentY = this.touchStartY;
      this.touchStartTime = Date.now();
      this.isSwiping = true;
    }

    handleTouchMove(e) {
      this.resetInactivityTimer();
      if (!this.isSwiping || this.activeModal) return;

      const touch = (e.touches && e.touches[0]) || (e.changedTouches && e.changedTouches[0]) || e;
      this.touchCurrentX = touch.clientX !== undefined ? touch.clientX : (touch.pageX || 0);
      this.touchCurrentY = touch.clientY !== undefined ? touch.clientY : (touch.pageY || 0);
    }

    handleTouchEnd(e) {
      this.resetInactivityTimer();
      if (!this.isSwiping || this.activeModal) {
        this.isSwiping = false;
        return;
      }
      this.isSwiping = false;

      const touch = (e.changedTouches && e.changedTouches[0]) || (e.touches && e.touches[0]) || e;
      const touchEndX = touch.clientX !== undefined ? touch.clientX : (this.touchCurrentX || 0);
      const touchEndY = touch.clientY !== undefined ? touch.clientY : (this.touchCurrentY || 0);
      const deltaX = touchEndX - this.touchStartX;
      const deltaY = touchEndY - this.touchStartY;
      const duration = Math.max(Date.now() - this.touchStartTime, 1);
      const absDeltaX = Math.abs(deltaX);
      const absDeltaY = Math.abs(deltaY);
      const velocity = absDeltaX / duration;

      if (absDeltaX >= this.swipeDistanceThreshold && velocity >= this.swipeVelocityThreshold && absDeltaX > absDeltaY * 1.2) {
        this.justSwiped = true;
        setTimeout(() => { this.justSwiped = false; }, 120);

        if (typeof e.stopPropagation === 'function') {
          e.stopPropagation();
        }

        if (deltaX < 0) {
          // Swipe Left -> next / forward
          this.pageNext();
        } else {
          // Swipe Right -> prev / backward
          this.pagePrev();
        }
      }
    }

    handleClick(e) {
      if (this.justSwiped) {
        this.justSwiped = false;
        return;
      }
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

    page(direction) {
      if (direction === 'next' || direction === 'forward') {
        return this.pageNext();
      }
      if (direction === 'prev' || direction === 'backward') {
        return this.pagePrev();
      }
      return false;
    }

    pageNext() {
      const cached = this.resolveCachedRange();

      if (this.activeView === 'day') {
        const nextDay = new Date(this.currentDate.getTime());
        nextDay.setDate(nextDay.getDate() + 1);
        const nextDayStr = this.formatDateISO(nextDay);
        if (cached && cached.end && nextDayStr > cached.end) {
          this.showSyncIndicator('Not synced yet');
          return false;
        }
        this.currentDate = nextDay;
        this.pageOffset += 1;
        this.hideSyncIndicator();
        this.updateDateUI();
        if (typeof this.options.onPageChange === 'function') {
          this.options.onPageChange('next', this.currentDate, this);
        }
        return true;
      }

      if (this.activeView === 'week') {
        const step = this.resolveDaysCount();
        const nextStart = new Date(this.currentWeekStart.getTime());
        nextStart.setDate(nextStart.getDate() + step);
        const nextStartStr = this.formatDateISO(nextStart);
        if (cached && cached.end && nextStartStr > cached.end) {
          this.showSyncIndicator('Not synced yet');
          return false;
        }
        this.currentWeekStart = nextStart;
        this.currentDate = new Date(nextStart.getTime());
        this.pageOffset += 1;
        this.hideSyncIndicator();
        this.updateDateUI();
        if (typeof this.options.onPageChange === 'function') {
          this.options.onPageChange('next', this.currentWeekStart, this);
        }
        return true;
      }

      if (this.activeView === 'month') {
        const nextMonth = new Date(this.currentDate.getTime());
        nextMonth.setDate(1);
        nextMonth.setMonth(nextMonth.getMonth() + 1);
        const nextMonthStr = this.formatDateISO(nextMonth).slice(0, 7);
        if (cached && cached.end && nextMonthStr > cached.end.slice(0, 7)) {
          this.showSyncIndicator('Not synced yet');
          return false;
        }
        this.currentDate = nextMonth;
        this.pageOffset += 1;
        this.hideSyncIndicator();
        this.updateDateUI();
        if (typeof this.options.onPageChange === 'function') {
          this.options.onPageChange('next', this.currentDate, this);
        }
        return true;
      }

      return false;
    }

    pagePrev() {
      const cached = this.resolveCachedRange();

      if (this.activeView === 'day') {
        const prevDay = new Date(this.currentDate.getTime());
        prevDay.setDate(prevDay.getDate() - 1);
        const prevDayStr = this.formatDateISO(prevDay);
        if (cached && cached.start && prevDayStr < cached.start) {
          this.showSyncIndicator('Not synced yet');
          return false;
        }
        this.currentDate = prevDay;
        this.pageOffset -= 1;
        this.hideSyncIndicator();
        this.updateDateUI();
        if (typeof this.options.onPageChange === 'function') {
          this.options.onPageChange('prev', this.currentDate, this);
        }
        return true;
      }

      if (this.activeView === 'week') {
        const step = this.resolveDaysCount();
        const prevStart = new Date(this.currentWeekStart.getTime());
        prevStart.setDate(prevStart.getDate() - step);
        const prevEnd = new Date(prevStart.getTime());
        prevEnd.setDate(prevEnd.getDate() + step - 1);
        const prevEndStr = this.formatDateISO(prevEnd);
        if (cached && cached.start && prevEndStr < cached.start) {
          this.showSyncIndicator('Not synced yet');
          return false;
        }
        this.currentWeekStart = prevStart;
        this.currentDate = new Date(prevStart.getTime());
        this.pageOffset -= 1;
        this.hideSyncIndicator();
        this.updateDateUI();
        if (typeof this.options.onPageChange === 'function') {
          this.options.onPageChange('prev', this.currentWeekStart, this);
        }
        return true;
      }

      if (this.activeView === 'month') {
        const prevMonth = new Date(this.currentDate.getTime());
        prevMonth.setDate(1);
        prevMonth.setMonth(prevMonth.getMonth() - 1);
        const prevMonthStr = this.formatDateISO(prevMonth).slice(0, 7);
        if (cached && cached.start && prevMonthStr < cached.start.slice(0, 7)) {
          this.showSyncIndicator('Not synced yet');
          return false;
        }
        this.currentDate = prevMonth;
        this.pageOffset -= 1;
        this.hideSyncIndicator();
        this.updateDateUI();
        if (typeof this.options.onPageChange === 'function') {
          this.options.onPageChange('prev', this.currentDate, this);
        }
        return true;
      }

      return false;
    }

    resetDate() {
      this.currentDate = new Date(this.baseDate.getTime());
      this.currentWeekStart = new Date(this.baseWeekStart.getTime());
      this.pageOffset = 0;
      this.hideSyncIndicator();
      this.updateDateUI();
    }

    showSyncIndicator(message = 'Not synced yet') {
      let indicator = this.element.querySelector('.cf-sync-indicator');
      if (!indicator) {
        indicator = document.createElement('div');
        indicator.className = 'cf-sync-indicator';
        indicator.setAttribute('role', 'status');
        indicator.setAttribute('aria-live', 'polite');
        const header = this.element.querySelector('.cf-header');
        if (header) {
          const heading = header.querySelector('.cf-date-heading') || header;
          heading.appendChild(indicator);
        } else {
          this.element.appendChild(indicator);
        }
      }
      indicator.textContent = message;
      indicator.style.display = '';

      if (this.syncIndicatorTimer) {
        clearTimeout(this.syncIndicatorTimer);
      }
      this.syncIndicatorTimer = setTimeout(() => {
        this.hideSyncIndicator();
      }, 2500);
      if (this.syncIndicatorTimer && typeof this.syncIndicatorTimer.unref === 'function') {
        this.syncIndicatorTimer.unref();
      }
    }

    hideSyncIndicator() {
      if (this.syncIndicatorTimer) {
        clearTimeout(this.syncIndicatorTimer);
        this.syncIndicatorTimer = null;
      }
      const indicator = this.element.querySelector('.cf-sync-indicator');
      if (indicator) {
        indicator.style.display = 'none';
      }
    }

    updateDateUI() {
      this.element.dataset.currentDate = this.formatDateISO(this.currentDate);
      this.element.dataset.pageOffset = String(this.pageOffset);

      if (this.activeView === 'day') {
        const label = this.labels.day || this.element.querySelector('.cf-range-label.cf-label-day') || this.element.querySelector('.cf-range-label');
        if (label) {
          label.textContent = this.formatDayLabel(this.currentDate);
        }
        if (this.todayBadge) {
          const isToday = this.formatDateISO(this.currentDate) === this.formatDateISO(this.baseDate);
          this.todayBadge.style.display = isToday ? '' : 'none';
        }
      } else if (this.activeView === 'week') {
        const label = this.labels.week || this.element.querySelector('.cf-range-label.cf-label-week') || this.element.querySelector('.cf-range-label');
        if (label) {
          const step = this.resolveDaysCount();
          const weekEnd = new Date(this.currentWeekStart.getTime());
          weekEnd.setDate(weekEnd.getDate() + step - 1);
          label.textContent = this.formatWeekLabel(this.currentWeekStart, weekEnd);
        }
        if (this.todayBadge) {
          this.todayBadge.style.display = 'none';
        }
      } else if (this.activeView === 'month') {
        const label = this.labels.month || this.element.querySelector('.cf-range-label.cf-label-month') || this.element.querySelector('.cf-range-label');
        if (label) {
          label.textContent = this.formatMonthLabel(this.currentDate);
        }
        if (this.todayBadge) {
          this.todayBadge.style.display = 'none';
        }
      }
    }

    formatDayLabel(d) {
      const weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
      const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
      return `${weekdays[d.getDay()]}, ${months[d.getMonth()]} ${d.getDate()}`;
    }

    formatWeekLabel(start, end) {
      const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
      if (start.getFullYear() === end.getFullYear()) {
        if (start.getMonth() === end.getMonth()) {
          return `${months[start.getMonth()]} ${start.getDate()} – ${end.getDate()}, ${start.getFullYear()}`;
        }
        return `${months[start.getMonth()]} ${start.getDate()} – ${months[end.getMonth()]} ${end.getDate()}, ${start.getFullYear()}`;
      }
      return `${months[start.getMonth()]} ${start.getDate()}, ${start.getFullYear()} – ${months[end.getMonth()]} ${end.getDate()}, ${end.getFullYear()}`;
    }

    formatMonthLabel(d) {
      const months = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
      return `${months[d.getMonth()]} ${d.getFullYear()}`;
    }

    formatDateISO(d) {
      if (!d) return '';
      const year = d.getFullYear();
      const month = String(d.getMonth() + 1).padStart(2, '0');
      const day = String(d.getDate()).padStart(2, '0');
      return `${year}-${month}-${day}`;
    }

    parseDateParts(str) {
      if (!str) return null;
      if (str instanceof Date) {
        return { year: str.getFullYear(), month: str.getMonth() + 1, day: str.getDate() };
      }
      const parts = String(str).split('T')[0].split('-');
      if (parts.length >= 3) {
        const y = parseInt(parts[0], 10);
        const m = parseInt(parts[1], 10);
        const d = parseInt(parts[2], 10);
        if (!isNaN(y) && !isNaN(m) && !isNaN(d)) {
          return { year: y, month: m, day: d };
        }
      }
      return null;
    }

    createLocalDate(year, month, day) {
      return new Date(year, month - 1, day, 12, 0, 0);
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

      // 5. Update TODAY badge visibility (visible only in Day view on today's date)
      if (this.todayBadge) {
        const isToday = this.formatDateISO(this.currentDate) === this.formatDateISO(this.baseDate);
        this.todayBadge.style.display = (view === 'day' && isToday) ? '' : 'none';
      }

      // 6. Refresh date labels for active view
      this.updateDateUI();
    }

    destroy() {
      if (this.inactivityTimer) {
        clearTimeout(this.inactivityTimer);
        this.inactivityTimer = null;
      }
      if (this.syncIndicatorTimer) {
        clearTimeout(this.syncIndicatorTimer);
        this.syncIndicatorTimer = null;
      }
      for (const btn of this.viewButtons) {
        btn.removeEventListener('click', this.clickHandler);
      }
      this.element.removeEventListener('click', this.clickHandler);
      this.element.removeEventListener('touchstart', this.touchStartHandler);
      this.element.removeEventListener('touchmove', this.touchMoveHandler);
      this.element.removeEventListener('touchend', this.touchEndHandler);
      const activityEvents = ['pointerdown', 'mousedown'];
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
      const widgetId = element.dataset.widgetId || element.dataset.widgetType || 'calendar-grid';
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
    window.MirrormereCalendarGrid = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = { CalendarFamilyInstance, MirrormereCalendarFamily: api, MirrormereCalendarGrid: api };
  }
})();
