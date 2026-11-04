const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

// Mock window and document environment for Node test runner
const carouselCode = fs.readFileSync(path.join(__dirname, '../static/js/carousel.js'), 'utf8');

function setupDOMMock(screenConfig) {
  global.window = {
    devicePixelRatio: 2,
    screen: screenConfig || { width: 1920, height: 1080 },
  };
  global.Image = class {
    constructor() {
      this.onload = null;
      this.src = '';
    }
  };

  // Evaluate carousel script in mock context
  const fn = new Function('window', 'document', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout', carouselCode);
  fn(global.window, {}, global.setInterval, global.clearInterval, global.setTimeout, global.clearTimeout);
}

test('Photo Carousel Client Controller', async (t) => {
  setupDOMMock();

  await t.test('computes dynamic edge-sizing query parameters from hardware display with dpr', () => {
    const mockElement = {
      dataset: { widgetId: 'test-photos' },
      querySelector(selector) {
        if (selector === '.photo-carousel-data') {
          return {
            textContent: JSON.stringify({
              cycle_interval_seconds: 30,
              photos: [
                { id: '1', url: 'https://lh3.googleusercontent.com/pw/ABC' },
                { id: '2', url: 'https://lh3.googleusercontent.com/pw/DEF' }
              ]
            })
          };
        }
        if (selector === '.photo-current' || selector === '.photo-next') {
          return { src: '', style: {} };
        }
        return null;
      },
      getBoundingClientRect() {
        return { width: 480, height: 320 };
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-photos');
    assert.ok(instance, 'instance should be registered');

    // Hardware display: 1920 * 2 = 3840, 1080 * 2 = 2160 (fit: contain)
    const sizedURL = instance.getSizedURL('https://lh3.googleusercontent.com/pw/ABC');
    assert.equal(sizedURL, 'https://lh3.googleusercontent.com/pw/ABC=w3840-h2160');

    window.MirrormerePhotoCarousel.unmount('test-photos');
    assert.equal(window.MirrormerePhotoCarousel.instances.has('test-photos'), false);
  });

  await t.test('appends crop flag when fit is cover', () => {
    const mockElement = {
      dataset: { widgetId: 'test-photos-cover', fit: 'cover' },
      querySelector(selector) {
        if (selector === '.photo-carousel-data') {
          return {
            textContent: JSON.stringify({
              photos: [{ id: '1', url: 'https://lh3.googleusercontent.com/pw/COVER' }]
            })
          };
        }
        if (selector === '.photo-current' || selector === '.photo-next') {
          return { src: '', style: {} };
        }
        return null;
      },
      getBoundingClientRect() {
        return { width: 480, height: 320 };
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-photos-cover');
    assert.ok(instance);

    // 480 * 2 = 960, 320 * 2 = 640 (fit: cover with -c crop flag)
    const sizedURL = instance.getSizedURL('https://lh3.googleusercontent.com/pw/COVER');
    assert.equal(sizedURL, 'https://lh3.googleusercontent.com/pw/COVER=w960-h640-c');

    window.MirrormerePhotoCarousel.unmount('test-photos-cover');
  });

  await t.test('falls back to hardware display dimensions when container has 0 size', () => {
    const mockElement = {
      dataset: { widgetId: 'test-photos-zero' },
      querySelector(selector) {
        if (selector === '.photo-carousel-data') {
          return {
            textContent: JSON.stringify({
              photos: [{ id: '1', url: 'https://lh3.googleusercontent.com/pw/XYZ' }]
            })
          };
        }
        return { src: '', style: {} };
      },
      getBoundingClientRect() {
        return { width: 0, height: 0 };
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-photos-zero');
    assert.ok(instance);

    const sizedURL = instance.getSizedURL('https://lh3.googleusercontent.com/pw/XYZ');
    // Defaults: 1920 * 2 = 3840, 1080 * 2 = 2160
    assert.equal(sizedURL, 'https://lh3.googleusercontent.com/pw/XYZ=w3840-h2160');

    window.MirrormerePhotoCarousel.unmount('test-photos-zero');
  });

  await t.test('respects explicit data-image-width and data-image-height overrides', () => {
    const mockElement = {
      dataset: { widgetId: 'test-photos-override', imageWidth: '1280', imageHeight: '720' },
      querySelector(selector) {
        if (selector === '.photo-carousel-data') {
          return {
            textContent: JSON.stringify({
              photos: [{ id: '1', url: 'https://lh3.googleusercontent.com/pw/OVERRIDE' }]
            })
          };
        }
        return { src: '', style: {} };
      },
      getBoundingClientRect() {
        return { width: 480, height: 320 };
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-photos-override');
    assert.ok(instance);

    const sizedURL = instance.getSizedURL('https://lh3.googleusercontent.com/pw/OVERRIDE');
    assert.equal(sizedURL, 'https://lh3.googleusercontent.com/pw/OVERRIDE=w1280-h720');

    window.MirrormerePhotoCarousel.unmount('test-photos-override');
  });

  await t.test('handles double-encoded JSON string data from legacy or SSR templates', () => {
    const rawData = {
      cycle_interval_seconds: 45,
      photos: [
        { id: 'p1', url: 'https://lh3.googleusercontent.com/pw/DOUBLE' }
      ]
    };
    const mockElement = {
      dataset: { widgetId: 'test-double-encoded' },
      querySelector(selector) {
        if (selector === '.photo-carousel-data') {
          // Double JSON-encoded string (e.g. "{\"photos\":[...]}")
          return {
            textContent: JSON.stringify(JSON.stringify(rawData))
          };
        }
        if (selector === '.photo-current' || selector === '.photo-next') {
          return { src: '', style: {} };
        }
        return null;
      },
      getBoundingClientRect() {
        return { width: 480, height: 320 };
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-double-encoded');
    assert.ok(instance);
    assert.equal(instance.photos.length, 1);
    assert.equal(instance.photos[0].id, 'p1');
    assert.equal(instance.cycleIntervalSeconds, 45);

    window.MirrormerePhotoCarousel.unmount('test-double-encoded');
  });

  await t.test('handles empty or malformed carousel data gracefully', () => {
    const mockElement = {
      dataset: { widgetId: 'test-empty' },
      querySelector() {
        return null;
      },
      getBoundingClientRect() {
        return { width: 100, height: 100 };
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-empty');
    assert.ok(instance);
    assert.equal(instance.photos.length, 0);

    window.MirrormerePhotoCarousel.unmount('test-empty');
  });

  await t.test('initializes and sizes ambient blurred backdrop layers with crop flag', () => {
    const currentImg = { src: '', style: {} };
    const nextImg = { src: '', style: {} };
    const currentBackdrop = { src: '', style: {} };
    const nextBackdrop = { src: '', style: {} };
    let clickHandler = null;

    const mockElement = {
      dataset: { widgetId: 'test-backdrop' },
      querySelector(selector) {
        if (selector === '.photo-carousel-data') {
          return {
            textContent: JSON.stringify({
              photos: [
                { id: 'b1', url: 'https://lh3.googleusercontent.com/pw/BACKDROP1' },
                { id: 'b2', url: 'https://lh3.googleusercontent.com/pw/BACKDROP2' }
              ]
            })
          };
        }
        if (selector === '.photo-current') return currentImg;
        if (selector === '.photo-next') return nextImg;
        if (selector === '.photo-backdrop-current') return currentBackdrop;
        if (selector === '.photo-backdrop-next') return nextBackdrop;
        return null;
      },
      getBoundingClientRect() {
        return { width: 600, height: 400 };
      },
      addEventListener(event, fn) {
        if (event === 'click') clickHandler = fn;
      },
      removeEventListener(event, fn) {
        if (event === 'click' && clickHandler === fn) clickHandler = null;
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-backdrop');
    assert.ok(instance);

    // Backdrop URL uses lightweight 96x54 thumbnail with -c crop flag for 32px Gaussian blur
    const backdropURL = instance.getBackdropURL('https://lh3.googleusercontent.com/pw/BACKDROP1');
    assert.equal(backdropURL, 'https://lh3.googleusercontent.com/pw/BACKDROP1=w96-h54-c');

    // On mount, current foreground is high-res and current backdrop is low-res thumbnail
    assert.equal(currentBackdrop.src, 'https://lh3.googleusercontent.com/pw/BACKDROP1=w96-h54-c');
    assert.equal(currentImg.src, 'https://lh3.googleusercontent.com/pw/BACKDROP1=w3840-h2160');

    // Tap to advance triggers cycle
    assert.ok(clickHandler, 'click listener should be attached');
    clickHandler();

    window.MirrormerePhotoCarousel.unmount('test-backdrop');
    assert.equal(clickHandler, null, 'click listener should be detached on unmount');
  });

  await t.test('skips backdrop URL generation and leaves backdrop layers unpopulated in cover fit mode', () => {
    const currentImg = { src: '', style: {} };
    const nextImg = { src: '', style: {} };
    const currentBackdrop = { src: '', style: {} };
    const nextBackdrop = { src: '', style: {} };
    let clickHandler = null;

    const mockElement = {
      dataset: { widgetId: 'test-backdrop-cover', fit: 'cover' },
      querySelector(selector) {
        if (selector === '.photo-carousel-data') {
          return {
            textContent: JSON.stringify({
              photos: [
                { id: 'c1', url: 'https://lh3.googleusercontent.com/pw/COVER1' },
                { id: 'c2', url: 'https://lh3.googleusercontent.com/pw/COVER2' }
              ]
            })
          };
        }
        if (selector === '.photo-current') return currentImg;
        if (selector === '.photo-next') return nextImg;
        if (selector === '.photo-backdrop-current') return currentBackdrop;
        if (selector === '.photo-backdrop-next') return nextBackdrop;
        return null;
      },
      getBoundingClientRect() {
        return { width: 600, height: 400 };
      },
      addEventListener(event, fn) {
        if (event === 'click') clickHandler = fn;
      },
      removeEventListener(event, fn) {
        if (event === 'click' && clickHandler === fn) clickHandler = null;
      }
    };

    window.MirrormerePhotoCarousel.mount(mockElement);
    const instance = window.MirrormerePhotoCarousel.instances.get('test-backdrop-cover');
    assert.ok(instance);

    // In cover fit mode, getBackdropURL returns empty string to prevent duplicate downloads
    const backdropURL = instance.getBackdropURL('https://lh3.googleusercontent.com/pw/COVER1');
    assert.equal(backdropURL, '');

    // Current backdrop src remains empty on mount
    assert.equal(currentBackdrop.src, '');
    assert.equal(currentImg.src, 'https://lh3.googleusercontent.com/pw/COVER1=w1200-h800-c');

    // Tap to advance should advance foreground without touching backdrop
    assert.ok(clickHandler, 'click listener should be attached');
    clickHandler();

    assert.equal(currentBackdrop.src, '');
    assert.equal(nextBackdrop.src, '');

    window.MirrormerePhotoCarousel.unmount('test-backdrop-cover');
  });

  await t.test('restores currentIndex to matching currentKey photo on remount and exhausts unseen photos before repeating', async () => {
    let lastPreloader = null;
    const OriginalImage = global.Image;
    global.Image = class {
      constructor() {
        this.onload = null;
        this.src = '';
        lastPreloader = this;
      }
    };

    try {
      const currentImg = { src: '', style: {} };
      const nextImg = { src: '', style: {} };
      const currentBackdrop = { src: '', style: {} };
      const nextBackdrop = { src: '', style: {} };

      const photos = [
        { id: '1', url: 'https://lh3.googleusercontent.com/pw/P1' },
        { id: '2', url: 'https://lh3.googleusercontent.com/pw/P2' },
        { id: '3', url: 'https://lh3.googleusercontent.com/pw/P3' },
      ];

      function createMockElement(widgetId) {
        return {
          dataset: { widgetId },
          querySelector(selector) {
            if (selector === '.photo-carousel-data') {
              return {
                textContent: JSON.stringify({
                  cycle_interval_seconds: 30,
                  photos,
                }),
              };
            }
            if (selector === '.photo-current') return currentImg;
            if (selector === '.photo-next') return nextImg;
            if (selector === '.photo-backdrop-current') return currentBackdrop;
            if (selector === '.photo-backdrop-next') return nextBackdrop;
            return null;
          },
          getBoundingClientRect() {
            return { width: 480, height: 320 };
          },
          addEventListener() {},
          removeEventListener() {},
        };
      }

      // Mount initial instance (photo P1, index 0)
      window.MirrormerePhotoCarousel.mount(createMockElement('w-deck-test'));
      let inst = window.MirrormerePhotoCarousel.instances.get('w-deck-test');
      assert.ok(inst);
      assert.equal(inst.currentIndex, 0);

      const deck = window.MirrormerePhotoCarousel.deckState.get('w-deck-test');
      assert.ok(deck);
      assert.equal(deck.currentKey, 'https://lh3.googleusercontent.com/pw/P1');
      assert.ok(deck.seenKeys.has('https://lh3.googleusercontent.com/pw/P1'));

      // Cycle 1: selects unseen photo P2 (index 1)
      inst.cycleNext();
      assert.ok(lastPreloader && typeof lastPreloader.onload === 'function');
      lastPreloader.onload();
      await new Promise((resolve) => setTimeout(resolve, 900));

      assert.equal(inst.currentIndex, 1);
      assert.equal(deck.currentKey, 'https://lh3.googleusercontent.com/pw/P2');
      assert.ok(deck.seenKeys.has('https://lh3.googleusercontent.com/pw/P2'));

      // Remount (e.g. screen rotation or handleWidgetUpdate)
      window.MirrormerePhotoCarousel.unmount('w-deck-test');
      assert.equal(window.MirrormerePhotoCarousel.instances.has('w-deck-test'), false);

      window.MirrormerePhotoCarousel.mount(createMockElement('w-deck-test'));
      inst = window.MirrormerePhotoCarousel.instances.get('w-deck-test');
      assert.ok(inst);
      // currentIndex restored to index 1 (photo P2)
      assert.equal(inst.currentIndex, 1);

      // Cycle 2: selects unseen photo P3 (index 2)
      inst.cycleNext();
      assert.ok(lastPreloader && typeof lastPreloader.onload === 'function');
      lastPreloader.onload();
      await new Promise((resolve) => setTimeout(resolve, 900));

      assert.equal(inst.currentIndex, 2);
      assert.equal(deck.currentKey, 'https://lh3.googleusercontent.com/pw/P3');
      assert.ok(deck.seenKeys.has('https://lh3.googleusercontent.com/pw/P3'));
      assert.equal(deck.seenKeys.size, 3);

      // Cycle 3: deck exhausted, clears seenKeys and cycles back to P1 (index 0)
      inst.cycleNext();
      assert.ok(lastPreloader && typeof lastPreloader.onload === 'function');
      lastPreloader.onload();
      await new Promise((resolve) => setTimeout(resolve, 900));

      assert.equal(inst.currentIndex, 0);
      assert.equal(deck.currentKey, 'https://lh3.googleusercontent.com/pw/P1');

      window.MirrormerePhotoCarousel.unmount('w-deck-test');
    } finally {
      global.Image = OriginalImage;
    }
  });
});

