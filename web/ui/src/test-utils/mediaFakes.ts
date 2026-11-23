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
