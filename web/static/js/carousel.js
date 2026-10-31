/**
 * Mirrormere Display Carousel & Layout Engine
 * Manages 6x2 CSS grid canvas rendering, DOM pre-warming, hot-swapping, and touch swipe navigation.
 */
class MirrormereCarousel {
  static instances = new Set();

  static pause() {
    for (const inst of MirrormereCarousel.instances) {
      inst.pause();
    }
  }

  static resume() {
    for (const inst of MirrormereCarousel.instances) {
      inst.resume();
    }
  }

  constructor(canvasElement, indicatorElement) {
    this.canvas = canvasElement || (typeof document !== 'undefined' && typeof document.getElementById === 'function' ? document.getElementById('grid-canvas') : null);
    this.indicator = indicatorElement || (typeof document !== 'undefined' && typeof document.getElementById === 'function' ? document.getElementById('screen-indicator') : null);
    this.currentScreen = 0;
    this.totalScreens = 1;
    this.activeWidgets = new Map(); // widget_id -> { element, origin, dimensions }
    this.touchStartX = 0;
    this.touchStartY = 0;
    this.touchStartTime = 0;
    this.touchIgnored = false;
    this.isTransitioning = false;
    this.isPaused = false;
    this.isNavigating = false;
    this.navLockTimeout = null;
    this._isDragging = false;
    this.pendingRotateData = null;
    this._localSwipeUntil = 0;
    this._initialScreenLocked = false;
    this.activeTaskWidgetId = 'whole-foods-tasks';
    this.taskWidgetIds = ['whole-foods-tasks', 'costco-tasks', 'ryans-tasks'];
    this.screenLayouts = new Map([
      [0, [
        { widget_id: 'hearth-family-grid', origin: [0, 0], dimensions: [2, 4] },
        { widget_id: 'whole-foods-tasks', origin: [0, 4], dimensions: [1, 2] },
        { widget_id: 'family-photos', origin: [1, 4], dimensions: [1, 2] },
      ]],
      [1, [
        { widget_id: 'youtube-music-card', origin: [0, 0], dimensions: [2, 3] },
        { widget_id: 'new-on-plex', origin: [0, 3], dimensions: [2, 3] },
      ]],
      [2, [
        { widget_id: 'home-assistant-dashboard', origin: [0, 0], dimensions: [2, 6] },
      ]],
    ]);
    this.widgetNodeCache = new Map();
    this._prewarmedAll = false;

    if (this.canvas && this.canvas.children) {
      for (const child of Array.from(this.canvas.children)) {
        const wid = child.dataset && child.dataset.widgetId;
        if (wid) {
          this.widgetNodeCache.set(wid, child);
        }
        child.style.display = 'none';
      }
    }

    MirrormereCarousel.instances.add(this);

    this.bindTouchGestures();
    this.bindIndicatorGestures();
    if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
      window.addEventListener('message', (e) => {
        if (e && e.data && e.data.type === 'hearth-swipe') {
          this.advanceScreen(e.data.direction || 'next');
        }
      });
    }
    setTimeout(() => {
      const initialWidgets = this.getWidgetsForScreen(0);
      if (initialWidgets) {
        this.totalScreens = 3;
        this.currentScreen = 0;
        this.updateIndicator();
        this.ensureWidgetsMounted(initialWidgets).then(() => {
          if (this.currentScreen === 0) {
            this.applyCachedScreen(0, this.getWidgetsForScreen(0));
            if (window.MirrormereDisplay && typeof window.MirrormereDisplay.scanAndIngestCalendarWidgets === 'function') {
              window.MirrormereDisplay.scanAndIngestCalendarWidgets();
              window.MirrormereDisplay.updateNextUpEvent();
            }
          }
          if (!this._prewarmedAll) {
            this._prewarmedAll = true;
            setTimeout(() => this.prewarmAllScreens(), 300);
          }
        });
      }
    }, 0);
  }

  /**
   * Set or re-bind the screen indicator element.
   */
  setIndicator(element) {
    this.indicator = element;
    this.bindIndicatorGestures();
    this.updateIndicator();
  }

  /**
   * Build the widget render URL. When the display page was opened with a
   * ?node=<voice node_id> query parameter, it is forwarded so per-display
   * widgets (chat-log) render that device's own conversation.
   */
  renderURL(widgetID) {
    let url = `api/widgets/${encodeURIComponent(widgetID)}/render`;
    try {
      const node = new URLSearchParams(window.location.search).get('node');
      if (node) {
        url += `?node=${encodeURIComponent(node)}`;
      }
    } catch (_) {
      // No window.location (non-browser test harness): render without a node.
    }
    return url;
  }

  /**
   * Pause carousel rotation and swipe handling during video presentation mode.
   */
  pause() {
    this.isPaused = true;
    if (this.indicator) {
      if (this.indicator.classList && typeof this.indicator.classList.add === 'function') {
        this.indicator.classList.add('paused');
      }
      if (this.indicator.style) {
        this.indicator.style.display = 'none';
      }
    }
  }

  /**
   * Resume carousel rotation and apply any pending rotation state.
   */
  resume() {
    this.isPaused = false;
    if (this.indicator && this.indicator.classList && typeof this.indicator.classList.remove === 'function') {
      this.indicator.classList.remove('paused');
    }
    if (this.pendingRotateData) {
      const data = this.pendingRotateData;
      this.pendingRotateData = null;
      this.handleScreenRotate(data);
    } else {
      this.updateIndicator();
    }
  }

  /**
   * Unmount outgoing widget controllers before DOM replacement.
   */
  unmountWidgetControllers(widgetId) {
    if (!widgetId || typeof window === 'undefined') return;
    for (const ctrl of [
      window.MirrormereCalendarFamily,
      window.MirrormereLiveView,
      window.MirrormerePhotoCarousel,
    ]) {
      if (ctrl && typeof ctrl.unmount === 'function') {
        ctrl.unmount(widgetId);
      }
    }
  }

  /**
   * React to screen.rotate event: pre-warms widget markup off-screen
   * and swaps fragments in a single frame to eliminate blank flashes.
   */
  getWidgetsForScreen(screenIndex) {
    if (screenIndex === 0) {
      return [
        { widget_id: 'hearth-family-grid', origin: [0, 0], dimensions: [2, 4] },
        { widget_id: this.activeTaskWidgetId || 'whole-foods-tasks', origin: [0, 4], dimensions: [1, 2] },
        { widget_id: 'family-photos', origin: [1, 4], dimensions: [1, 2] },
      ];
    }
    return this.screenLayouts.get(screenIndex) || [];
  }

  injectTaskSwitcher(node, wid) {
    if (!node || !this.taskWidgetIds.includes(wid)) return;
    const hdr = node.querySelector('.tasks-header');
    if (!hdr || hdr.querySelector('.hearth-task-switcher')) return;
    const bar = document.createElement('div');
    bar.className = 'hearth-task-switcher';
    bar.style.cssText = 'display:inline-flex;gap:4px;margin-left:auto;';
    const tabs = [
      { id: 'whole-foods-tasks', label: 'WF' },
      { id: 'costco-tasks', label: 'Costco' },
      { id: 'ryans-tasks', label: 'To-Do' },
    ];
    for (const t of tabs) {
      const b = document.createElement('button');
      b.type = 'button';
      b.textContent = t.label;
      const isAct = t.id === wid;
      b.style.cssText = `border:0;border-radius:999px;padding:3px 8px;font-size:0.68rem;font-weight:800;cursor:pointer;background:${isAct ? '#7c3aed' : 'rgba(148,163,184,0.22)'};color:${isAct ? '#ffffff' : 'inherit'};`;
      b.addEventListener('click', (ev) => {
        ev.stopPropagation();
        if (this.activeTaskWidgetId !== t.id) {
          this.activeTaskWidgetId = t.id;
          const wList = this.getWidgetsForScreen(0);
          this.ensureWidgetsMounted(wList).then(() => {
            if (this.currentScreen === 0) {
              this.applyCachedScreen(0, wList);
            }
          });
        }
      });
      bar.appendChild(b);
    }
    const prog = hdr.querySelector('.tasks-progress');
    if (prog) prog.style.display = 'none';
    hdr.appendChild(bar);
  }

  async handleScreenRotate(data) {
    if (!data || !Array.isArray(data.widgets)) {
      return;
    }

    this.releaseNavLock();

    if (this.isPaused) {
      this.pendingRotateData = data;
      return;
    }

    if (this._localSwipeUntil && Date.now() < this._localSwipeUntil) {
      return;
    }

    const targetScreen = !this._initialScreenLocked ? 0 : this.currentScreen;
    this._initialScreenLocked = true;
    this.currentScreen = targetScreen;
    this.totalScreens = 3;
    const widgetsToShow = this.getWidgetsForScreen(targetScreen);
    this.updateIndicator();

    await this.ensureWidgetsMounted(widgetsToShow);
    this.applyCachedScreen(targetScreen, widgetsToShow);

    if (!this._prewarmedAll) {
      this._prewarmedAll = true;
      setTimeout(() => this.prewarmAllScreens(), 400);
    }
  }

  async ensureWidgetsMounted(widgets) {
    if (!Array.isArray(widgets)) return;
    const missingWidgets = widgets.filter(w => !this.widgetNodeCache.has(w.widget_id));
    for (const w of widgets) {
      const existingNode = this.widgetNodeCache.get(w.widget_id);
      if (existingNode) {
        this.injectTaskSwitcher(existingNode, w.widget_id);
      }
    }
    if (missingWidgets.length === 0) return;

    const fetchPromises = missingWidgets.map(async (widget) => {
      try {
        const res = await fetch(this.renderURL(widget.widget_id));
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const html = await res.text();
        return { widget, html };
      } catch (err) {
        const fallbackHTML = `<div class="widget-card" data-widget-id="${widget.widget_id}"><div style="font-size:0.9rem;">Widget ${widget.widget_id} unavailable</div></div>`;
        return { widget, html: fallbackHTML };
      }
    });
    const fetched = await Promise.all(fetchPromises);
    for (const { widget, html } of fetched) {
      if (this.widgetNodeCache.has(widget.widget_id)) continue;
      const temp = document.createElement('div');
      temp.innerHTML = html.trim();
      const node = (temp.querySelector && temp.querySelector('.widget-card')) || temp.firstElementChild || document.createElement('div');
      if (temp.children) {
        for (const child of Array.from(temp.children)) {
          if (child !== node && (child.tagName === 'STYLE' || child.tagName === 'SCRIPT')) {
            node.appendChild(child);
          }
        }
      }
      node.dataset.widgetId = widget.widget_id;
      node.style.display = 'none';
      this.injectTaskSwitcher(node, widget.widget_id);
      if (this.canvas) {
        this.canvas.appendChild(node);
      }
      this.widgetNodeCache.set(widget.widget_id, node);
      if (node.classList && (node.classList.contains('widget-calendar-grid') || node.classList.contains('widget-calendar-family'))) {
        if (window.MirrormereCalendarFamily && typeof window.MirrormereCalendarFamily.mount === 'function') {
          window.MirrormereCalendarFamily.mount(node);
        }
      }
    }
  }

  applyCachedScreen(screenIndex, widgets) {
    if (!this.canvas || !Array.isArray(widgets)) return;
    const activeIds = new Set();
    const newActiveWidgets = new Map();

    for (const widget of widgets) {
      const node = this.widgetNodeCache.get(widget.widget_id);
      if (!node) continue;
      this.injectTaskSwitcher(node, widget.widget_id);
      const colStart = (widget.origin && widget.origin[0] !== undefined) ? widget.origin[0] + 1 : 1;
      const rowStart = (widget.origin && widget.origin[1] !== undefined) ? widget.origin[1] + 1 : 1;
      const colSpan = (widget.dimensions && widget.dimensions[0]) ? widget.dimensions[0] : 1;
      const rowSpan = (widget.dimensions && widget.dimensions[1]) ? widget.dimensions[1] : 1;

      node.style.gridColumn = `${colStart} / span ${colSpan}`;
      node.style.gridRow = `${rowStart} / span ${rowSpan}`;
      node.dataset.widgetId = widget.widget_id;
      node.dataset.originCol = String(colStart - 1);
      node.dataset.originRow = String(rowStart - 1);
      node.dataset.dimCols = String(colSpan);
      node.dataset.dimRows = String(rowSpan);
      node.style.display = '';

      activeIds.add(widget.widget_id);
      newActiveWidgets.set(widget.widget_id, {
        element: node,
        origin: widget.origin,
        dimensions: widget.dimensions,
      });

      if (typeof requestAnimationFrame === 'function') {
        requestAnimationFrame(() => {
          const calInst = window.MirrormereCalendarFamily && window.MirrormereCalendarFamily.getInstance && window.MirrormereCalendarFamily.getInstance(widget.widget_id);
          if (calInst && typeof calInst.fitDayEvents === 'function') {
            calInst.fitDayEvents();
          }
        });
      }
    }

    for (const [wid, node] of this.widgetNodeCache.entries()) {
      if (!activeIds.has(wid)) {
        node.style.display = 'none';
      }
    }
    if (this.canvas && this.canvas.children) {
      for (const child of Array.from(this.canvas.children)) {
        const wid = child.dataset && child.dataset.widgetId;
        if (!wid || !activeIds.has(wid)) {
          child.style.display = 'none';
        }
      }
    }

    this.activeWidgets = newActiveWidgets;
    this.canvas.dataset.widgetCount = String(newActiveWidgets.size);
    this.canvas.dataset.screenHydrated = 'true';
  }

  async prewarmAllScreens() {
    for (const [, widgets] of this.screenLayouts.entries()) {
      await this.ensureWidgetsMounted(widgets);
    }
    const extraTasks = this.taskWidgetIds.map(id => ({ widget_id: id, origin: [0, 4], dimensions: [1, 2] }));
    await this.ensureWidgetsMounted(extraTasks);
  }

  async handleWidgetUpdate(data) {
    if (!data || !data.widget_id || this.isPaused) {
      return;
    }

    const widgetID = data.widget_id;
    if (!this.activeWidgets.has(widgetID) && !this.widgetNodeCache.has(widgetID)) {
      return;
    }

    try {
      const res = await fetch(this.renderURL(widgetID));
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      const html = await res.text();
      const existing = this.canvas.querySelector(`[data-widget-id="${widgetID}"]`);
      if (existing) {
        const temp = document.createElement('div');
        temp.innerHTML = html.trim();
        const newNode = (temp.querySelector && temp.querySelector('.widget-card')) || temp.firstElementChild;
        if (newNode) {
          if (temp.children) {
            for (const child of Array.from(temp.children)) {
              if (child !== newNode && (child.tagName === 'STYLE' || child.tagName === 'SCRIPT')) {
                newNode.appendChild(child);
              }
            }
          }
          newNode.style.gridColumn = existing.style.gridColumn;
          newNode.style.gridRow = existing.style.gridRow;
          newNode.dataset.widgetId = widgetID;
          this.unmountWidgetControllers(widgetID);
          newNode.style.display = existing.style.display;
          this.injectTaskSwitcher(newNode, widgetID);
          existing.replaceWith(newNode);
          this.widgetNodeCache.set(widgetID, newNode);
          const cached = this.activeWidgets.get(widgetID);
          if (cached) {
            cached.element = newNode;
          }
        }
      }
    } catch (err) {
      console.warn(`[MirrormereCarousel] Error updating widget ${widgetID}:`, err);
    }
  }

  /**
   * React to widget.reload: reload all mounted instances matching the updated widget type.
   */
  async handleWidgetReload(data) {
    if (!data || !data.type || this.isPaused) {
      return;
    }

    const widgetType = data.type;
    const matchingElements = this.canvas.querySelectorAll(`[data-widget-type="${widgetType}"]`);
    for (const el of matchingElements) {
      const widgetID = el.dataset.widgetId;
      if (widgetID) {
        await this.handleWidgetUpdate({ widget_id: widgetID });
      }
    }
  }

  /**
   * Bind touch swipe gestures for Touch Kiosk Profile A.
   */
  bindTouchGestures() {
    const target = document.body || this.canvas;

    target.addEventListener('touchstart', (e) => {
      if (this.isPaused) return;
      if (e.target && e.target.closest && e.target.closest('.widget-calendar-grid, .widget-calendar-family, [data-prevent-screen-swipe]')) {
        this.touchIgnored = true;
        return;
      }
      this.touchIgnored = false;
      if (e.touches && e.touches.length === 1) {
        this.touchStartX = e.touches[0].clientX;
        this.touchStartY = e.touches[0].clientY;
        this.touchStartTime = Date.now();
      }
    }, { passive: true });

    target.addEventListener('touchend', (e) => {
      if (this.isPaused) return;
      if (this.touchIgnored) {
        this.touchIgnored = false;
        return;
      }
      if (e.target && e.target.closest && e.target.closest('.widget-calendar-grid, .widget-calendar-family, [data-prevent-screen-swipe]')) {
        return;
      }
      if (!e.changedTouches || e.changedTouches.length !== 1) {
        return;
      }

      const touchEndX = e.changedTouches[0].clientX;
      const touchEndY = e.changedTouches[0].clientY;
      const deltaX = touchEndX - this.touchStartX;
      const deltaY = touchEndY - this.touchStartY;
      const duration = Date.now() - this.touchStartTime;

      // Minimum swipe threshold: 60px horizontal, mostly horizontal direction, under 800ms
      if (duration < 800 && Math.abs(deltaX) > 60 && Math.abs(deltaX) > Math.abs(deltaY) * 1.5) {
        if (deltaX < 0) {
          // Swipe Left -> Advance to next screen
          this.advanceScreen('next');
        } else {
          // Swipe Right -> Return to previous screen
          this.advanceScreen('prev');
        }
      }
    }, { passive: true });
  }

  /**
   * Acquire navigation lock to debounce rapid swipes and taps.
   */
  acquireNavLock() {
    this.isNavigating = true;
    if (this.navLockTimeout) {
      clearTimeout(this.navLockTimeout);
    }
    this.navLockTimeout = setTimeout(() => {
      this.releaseNavLock();
    }, 180);
    if (this.navLockTimeout && typeof this.navLockTimeout.unref === 'function') {
      this.navLockTimeout.unref();
    }
  }

  /**
   * Release navigation lock and clear debounce timer.
   */
  releaseNavLock() {
    if (this.navLockTimeout) {
      clearTimeout(this.navLockTimeout);
      this.navLockTimeout = null;
    }
    this.isNavigating = false;
  }

  /**
   * Dispatch screen advance navigation request.
   */
  async advanceScreen(direction = 'next') {
    if (this.isPaused || this.totalScreens <= 1) return;
    const step = (direction === 'prev' || direction === 'backward') ? -1 : 1;
    const nextIdx = (this.currentScreen + step + this.totalScreens) % this.totalScreens;
    return this.selectScreen(nextIdx);
  }

  /**
   * Dispatch screen select navigation request.
   */
  async selectScreen(screenIndex) {
    if (this.isPaused || this.totalScreens <= 1 || screenIndex === this.currentScreen) return;
    this._localSwipeUntil = Date.now() + 2000;
    this.currentScreen = screenIndex;
    this.updateIndicator();
    const layout = this.getWidgetsForScreen(screenIndex);
    if (layout) {
      await this.ensureWidgetsMounted(layout);
      this.applyCachedScreen(screenIndex, layout);
    }
    this.acquireNavLock();
  }

  /**
   * Bind touch swipe gestures on the bottom screen indicator pill.
   */
  bindIndicatorGestures() {
    if (!this.indicator || typeof this.indicator.addEventListener !== 'function') return;
    let startX = 0;
    let startY = 0;
    let startTime = 0;

    this.indicator.addEventListener('touchstart', (e) => {
      if (this.isPaused) return;
      if (e.stopPropagation) e.stopPropagation();
      this._isDragging = false;
      if (e.touches && e.touches.length === 1) {
        startX = e.touches[0].clientX;
        startY = e.touches[0].clientY;
        startTime = Date.now();
      }
    }, { passive: true });

    this.indicator.addEventListener('touchmove', (e) => {
      if (this.isPaused) return;
      if (e.touches && e.touches.length === 1) {
        const dx = e.touches[0].clientX - startX;
        const dy = e.touches[0].clientY - startY;
        if (Math.abs(dx) > 10 || Math.abs(dy) > 10) {
          this._isDragging = true;
        }
      }
    }, { passive: true });

    this.indicator.addEventListener('touchend', (e) => {
      if (this.isPaused) return;
      if (e.stopPropagation) e.stopPropagation();
      if (!e.changedTouches || e.changedTouches.length !== 1) return;

      const endX = e.changedTouches[0].clientX;
      const endY = e.changedTouches[0].clientY;
      const deltaX = endX - startX;
      const deltaY = endY - startY;
      const duration = Date.now() - startTime;

      if (duration < 800 && Math.abs(deltaX) > 40 && Math.abs(deltaX) > Math.abs(deltaY) * 1.2) {
        this._isDragging = true;
        if (deltaX < 0) {
          this.advanceScreen('next');
        } else {
          this.advanceScreen('prev');
        }
        if (typeof e.preventDefault === 'function') {
          try { e.preventDefault(); } catch (_) {}
        }
        setTimeout(() => { this._isDragging = false; }, 200);
      } else {
        setTimeout(() => { this._isDragging = false; }, 50);
      }
    });
  }

  /**
   * Update or render screen indicator dots based on totalScreens and currentScreen.
   */
  updateIndicator() {
    if (!this.indicator && typeof document !== 'undefined' && typeof document.getElementById === 'function') {
      this.indicator = document.getElementById('screen-indicator');
      if (this.indicator) {
        this.bindIndicatorGestures();
      }
    }
    if (!this.indicator) {
      return;
    }

    if (this.totalScreens <= 1) {
      this.indicator.style.display = 'none';
      if (typeof this.indicator.replaceChildren === 'function') {
        this.indicator.replaceChildren();
      } else {
        this.indicator.innerHTML = '';
      }
      return;
    }

    this.indicator.style.display = 'inline-flex';

    // If existing child dot count matches totalScreens, reuse them without DOM recreation
    const existingDots = this.indicator.children ? Array.from(this.indicator.children) : [];
    if (existingDots.length === this.totalScreens) {
      existingDots.forEach((dot, idx) => {
        const isActive = idx === this.currentScreen;
        if (dot.classList && typeof dot.classList.toggle === 'function') {
          dot.classList.toggle('active', isActive);
        }
        if (typeof dot.setAttribute === 'function') {
          dot.setAttribute('aria-current', isActive ? 'step' : 'false');
          dot.setAttribute('aria-label', `Screen ${idx + 1} of ${this.totalScreens}`);
        }
      });
      return;
    }

    // Rebuild dots
    if (typeof this.indicator.replaceChildren === 'function') {
      this.indicator.replaceChildren();
    } else {
      this.indicator.innerHTML = '';
    }

    for (let i = 0; i < this.totalScreens; i++) {
      if (typeof document === 'undefined' || typeof document.createElement !== 'function') break;
      const dot = document.createElement('button');
      dot.type = 'button';
      dot.className = 'screen-indicator-dot' + (i === this.currentScreen ? ' active' : '');
      dot.setAttribute('aria-label', `Screen ${i + 1} of ${this.totalScreens}`);
      dot.setAttribute('aria-current', i === this.currentScreen ? 'step' : 'false');
      if (dot.dataset) {
        dot.dataset.screenIndex = String(i);
      }
      dot.addEventListener('click', (e) => {
        if (e && typeof e.stopPropagation === 'function') {
          e.stopPropagation();
        }
        if (this._isDragging) return;
        if (this.currentScreen !== i) {
          this.selectScreen(i);
        }
      });
      this.indicator.appendChild(dot);
    }
  }
}

// Attach globally
window.MirrormereCarousel = MirrormereCarousel;

/**
 * MirrormerePhotoCarousel Controller
 * Manages photo carousel instances, dynamic edge-resizing parameter injection (=w{w}-h{h}-c),
 * background image preloading, and smooth CSS cross-fade transitions.
 */
window.PhotoCarouselInstance = class PhotoCarouselInstance {
  constructor(element) {
    this.element = element;
    this.widgetId = element.dataset.widgetId;
    this.timer = null;
    this.currentIndex = 0;
    this.photos = [];
    this.cycleIntervalSeconds = 60;

    const dataScript = element.querySelector('.photo-carousel-data');
    if (dataScript) {
      try {
        let data = JSON.parse(dataScript.textContent);
        if (typeof data === 'string') {
          try {
            data = JSON.parse(data);
          } catch (_) {}
        }
        if (data && Array.isArray(data.photos)) {
          this.photos = data.photos;
        }
        if (data && data.cycle_interval_seconds) {
          this.cycleIntervalSeconds = data.cycle_interval_seconds;
        }
      } catch (e) {
        console.warn('[PhotoCarousel] Failed to parse carousel data:', e);
      }
    }

    this.currentImg = element.querySelector('.photo-current');
    this.nextImg = element.querySelector('.photo-next');
    this.currentBackdrop = element.querySelector('.photo-backdrop-current');
    this.nextBackdrop = element.querySelector('.photo-backdrop-next');
    this.fit = element.dataset.fit || 'contain';

    // Update first image with dynamic DOM sizing if container is measured
    this.updateCurrentSizing();

    // Tap/click to advance to next photo
    this._clickHandler = () => {
      if (this.photos.length > 1) {
        this.cycleNext();
        this.startCycling(); // reset interval timer on user interaction
      }
    };
    if (typeof this.element.addEventListener === 'function') {
      this.element.addEventListener('click', this._clickHandler);
    }

    if (this.photos.length > 1) {
      this.startCycling();
    }
  }

  getBackdropURL(rawURL) {
    if (!rawURL || this.fit === 'cover') return '';
    return `${rawURL}=w96-h54-c`;
  }

  getSizedURL(rawURL) {
    if (!rawURL) return '';
    const dpr = (typeof window !== 'undefined' && window.devicePixelRatio) || 1;
    const hardwareWidth = (typeof window !== 'undefined' && window.screen && window.screen.width) || (typeof window !== 'undefined' && window.innerWidth) || 1920;
    const hardwareHeight = (typeof window !== 'undefined' && window.screen && window.screen.height) || (typeof window !== 'undefined' && window.innerHeight) || 1080;
    const screenWidth = Math.round(hardwareWidth * dpr);
    const screenHeight = Math.round(hardwareHeight * dpr);

    let targetWidth;
    let targetHeight;

    if (this.element.dataset.imageWidth && this.element.dataset.imageHeight) {
      targetWidth = parseInt(this.element.dataset.imageWidth, 10);
      targetHeight = parseInt(this.element.dataset.imageHeight, 10);
    } else if (this.fit === 'cover') {
      const rect = this.element.getBoundingClientRect();
      targetWidth = Math.round((rect.width || 0) * dpr);
      targetHeight = Math.round((rect.height || 0) * dpr);
      if (targetWidth <= 0) targetWidth = screenWidth;
      if (targetHeight <= 0) targetHeight = screenHeight;
    } else {
      // Contain fit: pull display resolution from hardware to eliminate upscaling blur
      const rect = this.element.getBoundingClientRect();
      const rectWidth = Math.round((rect.width || 0) * dpr);
      const rectHeight = Math.round((rect.height || 0) * dpr);
      targetWidth = rectWidth > 0 ? Math.max(rectWidth, screenWidth) : screenWidth;
      targetHeight = rectHeight > 0 ? Math.max(rectHeight, screenHeight) : screenHeight;
    }

    const cropSuffix = this.fit === 'cover' ? '-c' : '';
    return `${rawURL}=w${targetWidth}-h${targetHeight}${cropSuffix}`;
  }

  updateCurrentSizing() {
    if (this.photos.length === 0) return;
    const first = this.photos[this.currentIndex];
    if (first && first.url) {
      if (this.currentImg) {
        const sized = this.getSizedURL(first.url);
        if (sized && !this.currentImg.src.includes(sized)) {
          this.currentImg.src = sized;
        }
      }
      if (this.currentBackdrop && this.fit !== 'cover') {
        const backdropSized = this.getBackdropURL(first.url);
        if (backdropSized && !this.currentBackdrop.src.includes(backdropSized)) {
          this.currentBackdrop.src = backdropSized;
        }
      }
    }
  }

  startCycling() {
    this.stopCycling();
    const intervalMs = Math.max(5000, this.cycleIntervalSeconds * 1000);
    this.timer = setInterval(() => {
      this.cycleNext();
    }, intervalMs);
    if (this.timer && typeof this.timer.unref === 'function') {
      this.timer.unref();
    }
  }

  stopCycling() {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  cycleNext() {
    if (this.element && this.element.isConnected === false) {
      this.destroy();
      return;
    }
    if (this.photos.length <= 1 || !this.currentImg || !this.nextImg) return;

    const nextIndex = (this.currentIndex + 1) % this.photos.length;
    const nextPhoto = this.photos[nextIndex];
    if (!nextPhoto || !nextPhoto.url) return;

    const nextSizedURL = this.getSizedURL(nextPhoto.url);
    const nextBackdropURL = this.getBackdropURL(nextPhoto.url);

    // Preload next image in memory
    const preloader = new Image();
    preloader.onload = () => {
      this.nextImg.src = nextSizedURL;
      this.nextImg.style.opacity = '1';
      this.currentImg.style.opacity = '0';

      if (this.nextBackdrop && this.currentBackdrop && this.fit !== 'cover' && nextBackdropURL) {
        this.nextBackdrop.src = nextBackdropURL;
        this.nextBackdrop.style.opacity = '1';
        this.currentBackdrop.style.opacity = '0';
      }

      setTimeout(() => {
        this.currentImg.src = nextSizedURL;
        this.currentImg.style.opacity = '1';
        this.nextImg.style.opacity = '0';
        this.nextImg.src = '';

        if (this.nextBackdrop && this.currentBackdrop && this.fit !== 'cover' && nextBackdropURL) {
          this.currentBackdrop.src = nextBackdropURL;
          this.currentBackdrop.style.opacity = '1';
          this.nextBackdrop.style.opacity = '0';
          this.nextBackdrop.src = '';
        }

        this.currentIndex = nextIndex;
      }, 850);
    };
    preloader.src = nextSizedURL;
  }

  destroy() {
    this.stopCycling();
    if (this._clickHandler && this.element && typeof this.element.removeEventListener === 'function') {
      this.element.removeEventListener('click', this._clickHandler);
      this._clickHandler = null;
    }
  }
}

window.MirrormerePhotoCarousel = {
  instances: new Map(),
  mount(element) {
    if (!element) return;
    const id = element.dataset.widgetId;
    if (!id) return;
    if (this.instances.has(id)) {
      this.instances.get(id).destroy();
      this.instances.delete(id);
    }
    const instance = new window.PhotoCarouselInstance(element);
    this.instances.set(id, instance);
  },
  unmount(id) {
    if (this.instances.has(id)) {
      this.instances.get(id).destroy();
      this.instances.delete(id);
    }
  }
};

