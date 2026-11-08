interface PhotoCarouselEmptyProps {
  source: 'share_url' | 'ember_bridge';
  needsAuth: boolean;
  syncError: boolean;
  albumUrl: string;
}

export function PhotoCarouselEmpty({ source, needsAuth, syncError, albumUrl }: PhotoCarouselEmptyProps) {
  let emptyTitle = 'No photos in shared album';
  if (source === 'ember_bridge') {
    if (needsAuth) emptyTitle = 'Google Photos sign-in required';
    else if (syncError) emptyTitle = 'Photo sync failed';
    else emptyTitle = 'No photos available';
  }

  const canConnect =
    source === 'ember_bridge' &&
    albumUrl.trim() !== '' &&
    typeof window !== 'undefined' &&
    typeof window.EmberBridge?.connectGooglePhotos === 'function';

  return (
    <div className="photo-empty">
      <span className="empty-icon">
        <svg
          width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor"
          strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"
        >
          <rect width="18" height="18" x="3" y="3" rx="2" ry="2" />
          <circle cx="9" cy="9" r="2" />
          <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
        </svg>
      </span>
      <span className="empty-title">{emptyTitle}</span>
      {canConnect && (
        <button
          type="button"
          className="photo-connect-btn"
          onClick={(e) => {
            e.stopPropagation();
            window.EmberBridge?.connectGooglePhotos?.(albumUrl);
          }}
        >
          Connect Google Photos
        </button>
      )}
    </div>
  );
}
