/** Receiver handed out by FakeRTCPeerConnection.addTransceiver; playoutDelayHint starts non-zero. */
export interface FakeRTCReceiver {
  playoutDelayHint: number | null;
  track: { kind: string; id: string };
}

/** Transceiver recorded by FakeRTCPeerConnection.addTransceiver. */
export interface FakeRTCTransceiver {
  kind: string;
  direction: string;
  init?: RTCRtpTransceiverInit | undefined;
  receiver: FakeRTCReceiver;
}

/** Track event fields the code under test reads; tests pass only the ones they need. */
export type FakeTrackEvent = Partial<Pick<RTCTrackEvent, 'streams' | 'track' | 'receiver'>>;

/** SDP returned by createOffer and stored by setLocalDescription. */
const FAKE_OFFER_SDP = 'v=0\r\no=mock\r\n';

type Listener = (event: Event) => void;

/**
 * Test double for RTCPeerConnection. Records transceivers, local/remote descriptions and close,
 * keeps addEventListener listeners in `listeners`, and lets tests drive state changes either by
 * assigning fields and calling the on* handler or via setConnectionState/setIceConnectionState.
 * Install with vi.stubGlobal('RTCPeerConnection', ...) or window.RTCPeerConnection; capture
 * instances with a subclass or factory. The SDP methods return already-resolved promises (one
 * microtask per await), which the stale-negotiation tests depend on.
 */
export class FakeRTCPeerConnection {
  connectionState: RTCPeerConnectionState = 'new';
  iceConnectionState: RTCIceConnectionState = 'new';
  iceGatheringState: RTCIceGatheringState = 'complete';
  localDescription: RTCSessionDescriptionInit | null = null;
  remoteDescription: RTCSessionDescriptionInit | null = null;
  transceivers: FakeRTCTransceiver[] = [];
  ontrack: ((event: FakeTrackEvent) => void) | null = null;
  onconnectionstatechange: ((event?: Event) => void) | null = null;
  oniceconnectionstatechange: ((event?: Event) => void) | null = null;
  onicecandidate: ((event: RTCPeerConnectionIceEvent) => void) | null = null;
  closed = false;
  listeners: Partial<Record<string, Listener[]>> = {};

  addEventListener(type: string, listener: Listener): void {
    (this.listeners[type] ??= []).push(listener);
  }

  removeEventListener(type: string, listener: Listener): void {
    const current = this.listeners[type];
    if (current) this.listeners[type] = current.filter((l) => l !== listener);
  }

  /** Calls the addEventListener listeners for `event` (an Event or a bare type name). */
  dispatchEvent(event: Event | string): boolean {
    const ev = typeof event === 'string' ? new Event(event) : event;
    for (const listener of [...(this.listeners[ev.type] ?? [])]) listener(ev);
    return true;
  }

  setConnectionState(state: RTCPeerConnectionState): void {
    this.connectionState = state;
    this.onconnectionstatechange?.(new Event('connectionstatechange'));
    this.dispatchEvent('connectionstatechange');
  }

  setIceConnectionState(state: RTCIceConnectionState): void {
    this.iceConnectionState = state;
    this.oniceconnectionstatechange?.(new Event('iceconnectionstatechange'));
    this.dispatchEvent('iceconnectionstatechange');
  }

  addTransceiver(kind: string, init?: RTCRtpTransceiverInit): FakeRTCTransceiver {
    const receiver: FakeRTCReceiver = { playoutDelayHint: 1, track: { kind, id: `${kind}-track` } };
    const transceiver = { kind, direction: init?.direction ?? 'sendrecv', init, receiver };
    this.transceivers.push(transceiver);
    return transceiver;
  }

  getReceivers(): FakeRTCReceiver[] {
    return this.transceivers.map((t) => t.receiver);
  }

  createOffer(): Promise<RTCSessionDescriptionInit> {
    return Promise.resolve({ type: 'offer', sdp: FAKE_OFFER_SDP });
  }

  setLocalDescription(desc: RTCSessionDescriptionInit): Promise<void> {
    this.localDescription = { type: desc.type, sdp: desc.sdp ?? '' };
    return Promise.resolve();
  }

  setRemoteDescription(desc: RTCSessionDescriptionInit): Promise<void> {
    this.remoteDescription = { type: desc.type, sdp: desc.sdp ?? '' };
    return Promise.resolve();
  }

  close(): void {
    this.closed = true;
    this.connectionState = 'closed';
    this.iceConnectionState = 'closed';
  }
}
