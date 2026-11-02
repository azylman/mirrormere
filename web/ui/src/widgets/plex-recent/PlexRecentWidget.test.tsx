import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PaneContext } from '../../carousel/PaneContext';
import { useDisplayStore } from '../../store';
import { PlexRecentWidget, type PlexItem, type SeerrResultItem } from './PlexRecentWidget';

const initialStore = useDisplayStore.getState();

const mockRecentItems: PlexItem[] = [
  {
    id: 'm-1',
    title: 'Dune: Part Two',
    subtitle: '2024',
    year: '2024',
    category: 'movie',
    type: 'movie',
    badge: 'MOVIE',
    rating: 8.5,
    thumb: 'https://example.com/dune.jpg',
    summary: 'Paul Atreides unites with Chani and the Fremen while seeking revenge.',
  },
  {
    id: 'tv-1',
    title: 'Fallout',
    subtitle: 'Season 1',
    year: '2024',
    category: 'tv',
    type: 'show',
    badge: 'TV',
    rating: 8.4,
    thumb: 'https://example.com/fallout.jpg',
    summary: 'In a future post-apocalyptic Los Angeles, citizens must live in underground bunkers.',
  },
  {
    id: 'ab-1',
    title: 'Project Hail Mary',
    subtitle: 'Andy Weir',
    year: '2021',
    category: 'audiobook',
    type: 'audiobook',
    badge: 'BOOK',
    rating: 4.9,
    thumb: 'https://example.com/phm.jpg',
    summary: 'Ryland Grace is the sole survivor on a desperate last-chance mission.',
  },
];

const mockUpcomingItems: PlexItem[] = [
  {
    id: 'up-1',
    title: 'Gladiator II',
    subtitle: 'Nov 22',
    year: '2024',
    category: 'upcoming',
    type: 'movie',
    badge: 'MOVIE',
    rating: 7.8,
    thumb: 'https://example.com/gladiator.jpg',
    summary: 'Years after witnessing the death of Maximus, Lucius must enter the Colosseum.',
  },
];

const mockSeerrResults: SeerrResultItem[] = [
  {
    id: 'sr-1',
    tmdb_id: 101,
    media_type: 'movie',
    title: 'The Batman',
    year: 2022,
    thumb: 'https://example.com/thebatman.jpg',
    status: 'available',
  },
  {
    id: 'sr-2',
    tmdb_id: 102,
    media_type: 'movie',
    title: 'Batman Begins',
    year: 2005,
    thumb: 'https://example.com/batmanbegins.jpg',
    status: 'requested',
  },
  {
    id: 'sr-3',
    tmdb_id: 103,
    media_type: 'movie',
    title: 'The Dark Knight',
    year: 2008,
    thumb: 'https://example.com/darkknight.jpg',
    status: 'pending',
  },
];

describe('PlexRecentWidget', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    useDisplayStore.setState(initialStore, true);
    useDisplayStore.setState({
      visibility: { visible: true },
      widgets: {
        'plex-1': {
          meta: {
            type: 'plex-recent',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          timestamp: '2026-11-01T12:00:00Z',
        },
      },
    });

    fetchMock = vi.fn(async (url: string | URL | Request) => {
      const urlStr = String(url);
      if (urlStr.includes('/plex/recent')) {
        return new Response(JSON.stringify({ items: mockRecentItems }), { status: 200 });
      }
      if (urlStr.includes('/plex/upcoming')) {
        return new Response(JSON.stringify({ items: mockUpcomingItems }), { status: 200 });
      }
      if (urlStr.includes('/plex/play')) {
        return new Response(JSON.stringify({ ok: true }), { status: 200 });
      }
      if (urlStr.includes('/seerr/search')) {
        return new Response(JSON.stringify({ items: mockSeerrResults }), { status: 200 });
      }
      if (urlStr.includes('/seerr/request')) {
        return new Response(JSON.stringify({ ok: true }), { status: 200 });
      }
      return new Response('{}', { status: 404 });
    });

    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('renders category tabs and filters items across movie, tv, audiobook, and upcoming categories', async () => {
    const { container } = render(<PlexRecentWidget id="plex-1" type="plex-recent" />);

    // Wait for initial fetch
    await waitFor(() => {
      const cards = container.querySelectorAll('#poster-grid .item-card');
      expect(cards.length).toBe(3);
    });

    // Default "All Recent" should show movie, tv, audiobook
    const titleElements = container.querySelectorAll('#poster-grid .title');
    const titles = Array.from(titleElements).map((el) => el.textContent);
    expect(titles).toEqual(['Dune: Part Two', 'Fallout', 'Project Hail Mary']);

    // Check tabs
    const tabs = container.querySelectorAll('.tab-btn');
    expect(tabs.length).toBe(5);

    // Switch to Movies
    const moviesTab = container.querySelector('.tab-btn[data-cat="movie"]') as HTMLButtonElement;
    expect(moviesTab).not.toBeNull();
    fireEvent.click(moviesTab);
    expect(moviesTab.classList.contains('active')).toBe(true);

    let currentTitles = Array.from(container.querySelectorAll('#poster-grid .title')).map((el) => el.textContent);
    expect(currentTitles).toEqual(['Dune: Part Two']);

    // Switch to TV Shows
    const tvTab = container.querySelector('.tab-btn[data-cat="tv"]') as HTMLButtonElement;
    fireEvent.click(tvTab);
    currentTitles = Array.from(container.querySelectorAll('#poster-grid .title')).map((el) => el.textContent);
    expect(currentTitles).toEqual(['Fallout']);

    // Switch to Audiobooks
    const audiobooksTab = container.querySelector('.tab-btn[data-cat="audiobook"]') as HTMLButtonElement;
    fireEvent.click(audiobooksTab);
    currentTitles = Array.from(container.querySelectorAll('#poster-grid .title')).map((el) => el.textContent);
    expect(currentTitles).toEqual(['Project Hail Mary']);

    // Switch to Upcoming ("Dropping Soon")
    const upcomingTab = container.querySelector('.tab-btn[data-cat="upcoming"]') as HTMLButtonElement;
    fireEvent.click(upcomingTab);
    currentTitles = Array.from(container.querySelectorAll('#poster-grid .title')).map((el) => el.textContent);
    expect(currentTitles).toEqual(['Gladiator II']);

    // Return to All Recent
    const allTab = container.querySelector('.tab-btn[data-cat="all"]') as HTMLButtonElement;
    fireEvent.click(allTab);
    currentTitles = Array.from(container.querySelectorAll('#poster-grid .title')).map((el) => el.textContent);
    expect(currentTitles).toEqual(['Dune: Part Two', 'Fallout', 'Project Hail Mary']);
  });

  it('opens detail modal on card click, displays metadata, and dispatches TV playback POST', async () => {
    const { container } = render(<PlexRecentWidget id="plex-1" type="plex-recent" />);

    await waitFor(() => {
      expect(container.querySelectorAll('#poster-grid .item-card').length).toBe(3);
    });

    const duneCard = container.querySelectorAll('#poster-grid .item-card')[0];
    fireEvent.click(duneCard);

    // Detail modal opens
    const modal = container.querySelector('#detail-modal');
    expect(modal?.classList.contains('open')).toBe(true);

    const titleEl = container.querySelector('#detail-title');
    expect(titleEl?.textContent).toBe('Dune: Part Two');

    const subEl = container.querySelector('#detail-sub');
    expect(subEl?.textContent).toBe('2024 · ★ 8.5');

    const summaryEl = container.querySelector('#detail-summary');
    expect(summaryEl?.textContent).toContain('Paul Atreides unites with Chani');

    // Living Room TV play button is present for movie
    const playBtn = container.querySelector('#tv-play-btn') as HTMLButtonElement;
    expect(playBtn).not.toBeNull();
    expect(playBtn.textContent).toContain('Play on Living Room TV');

    // Click Play button
    fireEvent.click(playBtn);

    await waitFor(() => {
      expect(playBtn.textContent).toBe('✓ Launching on Living Room TV');
    });

    // Verify POST payload to /hearth-api/plex/play
    const playCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes('/plex/play'));
    expect(playCalls.length).toBe(1);
    expect(playCalls[0][1]).toEqual({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        rating_key: 'm-1',
        title: 'Dune: Part Two',
        type: 'movie',
      }),
    });

    // Close detail modal
    const closeBtn = container.querySelector('#close-detail-btn') as HTMLButtonElement;
    fireEvent.click(closeBtn);
    expect(modal?.classList.contains('open')).toBe(false);
  });

  it('hides Living Room TV playback button for audiobook and upcoming categories', async () => {
    const { container } = render(<PlexRecentWidget id="plex-1" type="plex-recent" />);

    await waitFor(() => {
      expect(container.querySelectorAll('#poster-grid .item-card').length).toBe(3);
    });

    // Click Audiobook card
    const audioCard = container.querySelectorAll('#poster-grid .item-card')[2];
    fireEvent.click(audioCard);

    const modal = container.querySelector('#detail-modal');
    expect(modal?.classList.contains('open')).toBe(true);
    expect(container.querySelector('#detail-title')?.textContent).toBe('Project Hail Mary');
    // TV play button must NOT be present
    expect(container.querySelector('#tv-play-btn')).toBeNull();

    // Close modal
    fireEvent.click(container.querySelector('#close-detail-btn') as HTMLButtonElement);

    // Switch to Upcoming tab
    const upcomingTab = container.querySelector('.tab-btn[data-cat="upcoming"]') as HTMLButtonElement;
    fireEvent.click(upcomingTab);

    // Click Upcoming card
    const upCard = container.querySelector('#poster-grid .item-card') as HTMLElement;
    fireEvent.click(upCard);
    expect(container.querySelector('#detail-title')?.textContent).toBe('Gladiator II');
    // TV play button must NOT be present
    expect(container.querySelector('#tv-play-btn')).toBeNull();
  });

  it('opens Seerr modal, executes search, displays statuses, and handles request POST', async () => {
    const { container } = render(<PlexRecentWidget id="plex-1" type="plex-recent" />);

    // Click + Request button
    const openReqBtn = container.querySelector('#open-seerr-btn') as HTMLButtonElement;
    fireEvent.click(openReqBtn);

    const seerrModal = container.querySelector('#seerr-modal');
    expect(seerrModal?.classList.contains('open')).toBe(true);

    const seerrInput = container.querySelector('#seerr-input') as HTMLInputElement;
    fireEvent.change(seerrInput, { target: { value: 'Batman' } });
    expect(seerrInput.value).toBe('Batman');

    // Submit search form
    const form = container.querySelector('#seerr-form') as HTMLFormElement;
    fireEvent.submit(form);

    await waitFor(() => {
      const searchCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes('/seerr/search'));
      expect(searchCalls.length).toBe(1);
      expect(String(searchCalls[0][0])).toContain('/seerr/search?q=Batman');
    });

    await waitFor(() => {
      const resultCards = container.querySelectorAll('#seerr-results .item-card');
      expect(resultCards.length).toBe(3);
    });

    const resultButtons = container.querySelectorAll<HTMLButtonElement>('#seerr-results .req-action-btn');
    expect(resultButtons.length).toBe(3);

    // Item 101: available -> "✓ In Plex", disabled
    expect(resultButtons[0].textContent).toBe('✓ In Plex');
    expect(resultButtons[0].disabled).toBe(true);

    // Item 102: requested -> "✓ Requested", disabled
    expect(resultButtons[1].textContent).toBe('✓ Requested');
    expect(resultButtons[1].disabled).toBe(true);

    // Item 103: pending -> "+ Request", enabled
    expect(resultButtons[2].textContent).toBe('+ Request');
    expect(resultButtons[2].disabled).toBe(false);

    // Click request button on item 103
    fireEvent.click(resultButtons[2]);

    await waitFor(() => {
      expect(resultButtons[2].textContent).toBe('✓ Requested');
      expect(resultButtons[2].disabled).toBe(true);
    });

    // Verify request POST payload
    const reqCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes('/seerr/request'));
    expect(reqCalls.length).toBe(1);
    expect(reqCalls[0][1]).toEqual({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        media_type: 'movie',
        tmdb_id: 103,
      }),
    });

    // Close Seerr modal
    const closeSeerrBtn = container.querySelector('#close-seerr-btn') as HTMLButtonElement;
    fireEvent.click(closeSeerrBtn);
    expect(seerrModal?.classList.contains('open')).toBe(false);
  });

  it('respects custom api_base configured in widget meta', async () => {
    useDisplayStore.setState({
      widgets: {
        'plex-custom': {
          meta: {
            type: 'plex-recent',
            origin: [0, 0],
            dimensions: [3, 2],
            config: { api_base: '/custom-gateway' },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          timestamp: '2026-11-01T12:00:00Z',
        },
      },
    });

    render(<PlexRecentWidget id="plex-custom" type="plex-recent" />);

    await waitFor(() => {
      const urls = fetchMock.mock.calls.map(([url]) => String(url));
      expect(urls.some((u) => u.startsWith('/custom-gateway/plex/recent'))).toBe(true);
      expect(urls.some((u) => u.startsWith('/custom-gateway/plex/upcoming'))).toBe(true);
    });
  });

  it('pauses polling when off-pane or hidden, and resumes when active', async () => {
    vi.useFakeTimers();

    // 1. Off-pane test: isCurrentPane = false
    const { rerender } = render(
      <PaneContext.Provider value={{ isCurrentPane: false, paneIndex: 1 }}>
        <PlexRecentWidget id="plex-1" type="plex-recent" />
      </PaneContext.Provider>
    );

    // No fetch should occur while off-pane
    expect(fetchMock).not.toHaveBeenCalled();

    // Advance 180s while off-pane: still no fetch
    act(() => {
      vi.advanceTimersByTime(180000);
    });
    expect(fetchMock).not.toHaveBeenCalled();

    // 2. Rerender with active pane: fetch fires immediately
    rerender(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <PlexRecentWidget id="plex-1" type="plex-recent" />
      </PaneContext.Provider>
    );

    await act(async () => {
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(2); // recent + upcoming
    const initialCalls = fetchMock.mock.calls.length;

    // Advance 180s while active: interval re-fetches
    await act(async () => {
      vi.advanceTimersByTime(180000);
      await Promise.resolve();
    });

    expect(fetchMock.mock.calls.length).toBe(initialCalls + 2);

    // 3. Document visibility hidden: polling pauses
    act(() => {
      useDisplayStore.setState({ visibility: { visible: false } });
    });

    const callsBeforeHidden = fetchMock.mock.calls.length;
    act(() => {
      vi.advanceTimersByTime(180000);
    });
    // Should not have fetched again while document was hidden
    expect(fetchMock.mock.calls.length).toBe(callsBeforeHidden);

    vi.useRealTimers();
  });
});
