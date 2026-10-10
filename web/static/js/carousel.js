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

  constructor(canvasElement) {
    this.canvas = canvasElement || document.getElementById('grid-canvas');
    this.currentScreen = 0;
    this.totalScreens = 1;
    this.activeWidgets = new Map(); // widget_id -> { element, origin, dimensions }
    this.touchStartX = 0;
    this.touchStartY = 0;
    this.touchStartTime = 0;
    this.touchIgnored = false;
    this.isTransitioning = false;
    this.isPaused = false;
    this.pendingRotateData = null;

    MirrormereCarousel.instances.add(this);

    this.bindTouchGestures();
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
  }

  /**
   * Resume carousel rotation and apply any pending rotation state.
   */
  resume() {
    this.isPaused = false;
    if (this.pendingRotateData) {
      const data = this.pendingRotateData;
      this.pendingRotateData = null;
      this.handleScreenRotate(data);
    }
  }

  /**
   * React to screen.rotate event: pre-warms widget markup off-screen
   * and swaps fragments in a single frame to eliminate blank flashes.
   */
  async handleScreenRotate(data) {
    if (!data || !Array.isArray(data.widgets)) {
      return;
    }

    if (this.isPaused) {
      this.pendingRotateData = data;
      return;
    }

    this.currentScreen = data.current_screen ?? 0;
    this.totalScreens = data.total_screens ?? 1;

    // 1. Pre-warm: fetch all widget fragments in parallel before touching the active DOM
    const fetchPromises = data.widgets.map(async (widget) => {
      try {
        const res = await fetch(this.renderURL(widget.widget_id));
        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }
        const html = await res.text();
        return { widget, html, error: null };
      } catch (err) {
        console.warn(`[MirrormereCarousel] Failed to render widget ${widget.widget_id}:`, err);
        const fallbackHTML = `<div class="widget-card" data-widget-id="${widget.widget_id}" style="border-color: var(--mm-status-error);">
          <div style="color: var(--mm-status-error); font-size: 0.9rem;">Widget ${widget.widget_id} unavailable</div>
        </div>`;
        return { widget, html: fallbackHTML, error: err };
      }
    });

    const rendered = await Promise.all(fetchPromises);

    // 2. Build incoming fragment nodes
    const fragment = document.createDocumentFragment();
    const newActiveWidgets = new Map();

    for (const item of rendered) {
      const { widget, html } = item;
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

      // Enforce 6x2 grid cell positioning (1-indexed CSS grid lines)
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

      fragment.appendChild(node);
      newActiveWidgets.set(widget.widget_id, {
        element: node,
        origin: widget.origin,
        dimensions: widget.dimensions,
      });
    }

    // 3. Hot-swap onto canvas with smooth transition
    if (this.canvas) {
      this.canvas.classList.add('transitioning');
      // Single DOM replacement eliminates intermediate blank frames
      this.canvas.innerHTML = '';
      this.canvas.appendChild(fragment);
      this.activeWidgets = newActiveWidgets;
      // Hydration marker for the e-ink renderer's settle poll: lets it tell
      // "hydrated with N widgets" (N may be 0) from the pre-hydration page.
      this.canvas.dataset.widgetCount = String(newActiveWidgets.size);
      this.canvas.dataset.screenHydrated = 'true';

      requestAnimationFrame(() => {
        setTimeout(() => {
          if (this.canvas) {
            this.canvas.classList.remove('transitioning');
          }
        }, 150);
      });
    }
  }

  /**
   * React to widget.update: if widget is visible on active screen,
   * fetches fresh markup and updates the element in place.
   */
  async handleWidgetUpdate(data) {
    if (!data || !data.widget_id || this.isPaused) {
      return;
    }

    const widgetID = data.widget_id;
    if (!this.activeWidgets.has(widgetID)) {
      // Widget is rotated off-screen; skip DOM fetch
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
          existing.replaceWith(newNode);

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
    const target = this.canvas || document.body;

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
   * Dispatch screen advance navigation request.
   */
  async advanceScreen(direction = 'next') {
    try {
      await fetch('api/screen/advance', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ direction }),
      });
    } catch (err) {
      console.error('[MirrormereCarousel] Failed to advance screen:', err);
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
class PhotoCarouselInstance {
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
        const data = JSON.parse(dataScript.textContent);
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
    this.fit = element.dataset.fit || 'contain';

    // Update first image with dynamic DOM sizing if container is measured
    this.updateCurrentSizing();

    if (this.photos.length > 1) {
      this.startCycling();
    }
  }

  getSizedURL(rawURL) {
    if (!rawURL) return '';
    const rect = this.element.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    let targetWidth = Math.round((rect.width || 960) * dpr);
    let targetHeight = Math.round((rect.height || 640) * dpr);
    if (targetWidth <= 0) targetWidth = 960;
    if (targetHeight <= 0) targetHeight = 640;
    const cropSuffix = this.fit === 'cover' ? '-c' : '';
    return `${rawURL}=w${targetWidth}-h${targetHeight}${cropSuffix}`;
  }

  updateCurrentSizing() {
    if (!this.currentImg || this.photos.length === 0) return;
    const first = this.photos[this.currentIndex];
    if (first && first.url) {
      const sized = this.getSizedURL(first.url);
      if (sized && !this.currentImg.src.includes(sized)) {
        this.currentImg.src = sized;
      }
    }
  }

  startCycling() {
    this.stopCycling();
    const intervalMs = Math.max(5000, this.cycleIntervalSeconds * 1000);
    this.timer = setInterval(() => {
      this.cycleNext();
    }, intervalMs);
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

    // Preload next image in memory
    const preloader = new Image();
    preloader.onload = () => {
      this.nextImg.src = nextSizedURL;
      this.nextImg.style.opacity = '1';
      this.currentImg.style.opacity = '0';

      setTimeout(() => {
        this.currentImg.src = nextSizedURL;
        this.currentImg.style.opacity = '1';
        this.nextImg.style.opacity = '0';
        this.nextImg.src = '';
        this.currentIndex = nextIndex;
      }, 850);
    };
    preloader.src = nextSizedURL;
  }

  destroy() {
    this.stopCycling();
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
    const instance = new PhotoCarouselInstance(element);
    this.instances.set(id, instance);
  },
  unmount(id) {
    if (this.instances.has(id)) {
      this.instances.get(id).destroy();
      this.instances.delete(id);
    }
  }
};

