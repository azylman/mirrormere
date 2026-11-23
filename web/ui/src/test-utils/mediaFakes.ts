export { FakeRTCPeerConnection } from './fakeRTCPeerConnection';

export class MockMediaStream {
  tracks: unknown[] = [];
  constructor(tracks?: unknown[]) {
    if (tracks) this.tracks = [...tracks];
  }
}

export function createFakeTrack(kind = 'video', id = 'track-1'): MediaStreamTrack {
  return { id, kind } as MediaStreamTrack;
}

export function installPointerEventPolyfill(): void {
  if (typeof window.PointerEvent === 'undefined') {
    // @ts-expect-error polyfill PointerEvent for JSDOM
    window.PointerEvent = class PointerEvent extends MouseEvent {
      pointerId: number;
      width: number;
      height: number;
      pressure: number;
      pointerType: string;
      isPrimary: boolean;
      changedTouches?: { clientX: number; clientY: number }[];

      constructor(type: string, params: PointerEventInit & { changedTouches?: { clientX: number; clientY: number }[] } = {}) {
        super(type, params);
        this.pointerId = params.pointerId ?? 0;
        this.width = params.width ?? 1;
        this.height = params.height ?? 1;
        this.pressure = params.pressure ?? 0;
        this.pointerType = params.pointerType ?? '';
        this.isPrimary = params.isPrimary ?? false;
        if (params.changedTouches) this.changedTouches = params.changedTouches;
      }
    };
  }
}
