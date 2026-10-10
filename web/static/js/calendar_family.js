/**
 * Mirrormere Calendar Family Widget Controller
 * Manages 3-way view switching (Day | Week | Month), active view state synchronization,
 * and touch event handling for the Skylight family calendar widget.
 */
(() => {
  class CalendarFamilyInstance {
    constructor(element, options = {}) {
      this.element = element;
      this.options = options;
      this.widgetId = element.dataset.widgetId || 'calendar-family';
      this.data = this.parseData();
      this.config = this.parseConfig();
      this.defaultView = this.resolveDefaultView();
      this.activeView = this.resolveActiveView();

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
      this.updateViewUI(this.activeView);
    }

    bindViewSwitcher() {
      for (const btn of this.viewButtons) {
        btn.addEventListener('click', this.clickHandler);
      }
    }

    handleClick(e) {
      const btn = e.currentTarget || (e.target && e.target.closest && e.target.closest('.cf-view-btn'));
      if (!btn) return;
      const targetView = (btn.dataset.view || '').toLowerCase();
      if (targetView && (targetView === 'day' || targetView === 'week' || targetView === 'month')) {
        this.switchView(targetView);
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
      for (const btn of this.viewButtons) {
        btn.removeEventListener('click', this.clickHandler);
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
