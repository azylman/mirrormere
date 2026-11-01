import { startTransition, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useDisplayStore } from '../store';
import type { WidgetPlacement } from '../sse/types';
import { CarouselController, neighbourOf, type CarouselHost } from './controller';
import { Screen } from './Screen';
import { ScreenIndicator } from './ScreenIndicator';

const NO_PLACEMENTS: readonly WidgetPlacement[] = [];

interface Pane {
  index: number;
  offset: -1 | 0 | 1;
}

/** The panes mounted around `shown`: previous, current and next. Only one screen when there is no swipe. */
export function panesFor(shown: number, total: number): Pane[] {
  if (total <= 1) return [{ index: shown, offset: 0 }];
  return [
    { index: neighbourOf(shown, total, 'prev'), offset: -1 },
    { index: shown, offset: 0 },
    { index: neighbourOf(shown, total, 'next'), offset: 1 },
  ];
}

/**
 * Keys panes by screen index, so a neighbour that becomes current keeps its DOM and
 * its mounted widgets. With two screens, previous and next are the same screen, so the
 * previous pane gets its own key.
 */
export function paneKey(pane: Pane, total: number): string {
  return pane.offset === -1 && total === 2 ? `${pane.index}-prev` : String(pane.index);
}

/**
 * Tells the server which screen the user picked. The store takes the pick up front, so
 * the carousel and the store agree; a failed select puts the previous screen back, and
 * the store subscriber slides the carousel back to it.
 */
function selectScreen(index: number): void {
  const store = useDisplayStore.getState();
  const previous = store.screen.currentScreen;
  store.setCurrentScreen(index);
  fetch('api/screen/select', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ screen_index: index }),
  })
    .then((res) => {
      if (!res.ok) throw new Error(`screen select: HTTP ${res.status}`);
    })
    .catch((err: unknown) => {
      const s = useDisplayStore.getState();
      // Revert only when nothing newer happened (a rotate or another pick moves currentScreen).
      if (s.screen.currentScreen === index) s.setCurrentScreen(previous < s.screen.totalScreens ? previous : 0);
      console.error('[mirrormere] screen select failed:', err);
    });
}

export interface CarouselProps {
  hidden?: boolean;
}

/**
 * Three screen panes side by side (previous, current, next) in a track that follows
 * the finger. The current pane owns #grid-canvas. React renders panes only when the
 * shown screen or the layout changes; gestures and settles never go through React.
 */
export function Carousel({ hidden }: CarouselProps = {}) {
  const total = useDisplayStore((s) => s.screen.totalScreens);
  const layout = useDisplayStore((s) => s.layout);
  const layoutVersion = useDisplayStore((s) => s.layoutVersion);
  const paused = useDisplayStore((s) => s.carousel.paused);
  const videoMode = useDisplayStore(
    (s) => (s.video?.mode || (s.video?.primary ? 'video' : 'widgets')) === 'video',
  );
  const isHidden = hidden ?? videoMode;
  const [shown, setShown] = useState(() => useDisplayStore.getState().screen.currentScreen);
  // The screen React rendered. The controller starts from this, not from the store: a hydration
  // batch can move the store between this render and the effect that builds the controller.
  const shownRef = useRef(shown);

  const viewportRef = useRef<HTMLDivElement>(null);
  const trackRef = useRef<HTMLDivElement>(null);
  const controllerRef = useRef<CarouselController | null>(null);

  useEffect(() => {
    useDisplayStore.getState().loadScreens().catch((err: unknown) => {
      console.warn('[mirrormere] screen layout load failed:', err);
    });
  }, [layoutVersion]);

  useEffect(() => {
    const viewport = viewportRef.current;
    const track = trackRef.current;
    if (!viewport || !track) return;
    // Both updates leave the transitionend listener's task. The pane swap renders as a transition
    // (it yields, and the layout effect below still calls controller.rendered when it commits).
    // The store write and the POST wait one task, so the subscribers' render does not stack on
    // the listener; the controller treats the echo of that write as a no-op.
    const host: CarouselHost = {
      commit: (index) => startTransition(() => setShown(index)),
      select: (index) => {
        setTimeout(() => selectScreen(index), 0);
      },
    };
    const controller = new CarouselController({
      viewport,
      track,
      host,
      initial: shownRef.current,
    });
    controllerRef.current = controller;
    controller.attach();
    // The store moved after the first render: show its screen now, since the subscriber
    // attached above only hears later changes.
    const storeScreen = useDisplayStore.getState().screen.currentScreen;
    if (storeScreen !== shownRef.current) controller.requestShow(storeScreen);
    return () => {
      controller.dispose();
      controllerRef.current = null;
    };
  }, []);

  // Runs before paint, in the same commit that moved the panes: the track returns to
  // rest with the new current pane already in place, so nothing flashes.
  useLayoutEffect(() => {
    shownRef.current = shown;
    controllerRef.current?.rendered(shown);
  }, [shown]);

  const placementsByIndex = useMemo(() => {
    const map = new Map<number, readonly WidgetPlacement[]>();
    for (const screen of layout) map.set(screen.index, screen.widgets);
    return map;
  }, [layout]);

  const panes = panesFor(shown, total);
  const ready = layout.length > 0;

  return (
    <>
      <div
        ref={viewportRef}
        className={isHidden ? 'carousel-viewport is-hidden' : 'carousel-viewport'}
        style={isHidden ? { display: 'none' } : undefined}
      >
        <div ref={trackRef} className="carousel-track">
          {panes.map((pane) => (
            <Screen
              key={paneKey(pane, total)}
              index={pane.index}
              offset={pane.offset}
              placements={placementsByIndex.get(pane.index) ?? NO_PLACEMENTS}
              ready={ready}
            />
          ))}
        </div>
      </div>
      <ScreenIndicator
        total={total}
        active={shown}
        hidden={paused || isHidden}
        onSelect={(index) => controllerRef.current?.goTo(index)}
        onSwipe={(direction) => {
          const controller = controllerRef.current;
          if (controller) controller.goTo(neighbourOf(controller.current, total, direction));
        }}
      />
    </>
  );
}
