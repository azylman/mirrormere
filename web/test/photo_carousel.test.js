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
});
