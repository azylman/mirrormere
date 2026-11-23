package provider

import (
	"net/http"

	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

// NewTasksProviderWithAdapter constructs a TasksProvider with an injected Adapter for testing.
func NewTasksProviderWithAdapter(adapter adapters.Adapter) *TasksProvider {
	return &TasksProvider{
		adapter: adapter,
	}
}

// WithPhotosTransport swaps the photos HTTP client's transport so tests can
// answer off-loopback redirect hops without leaving the host.
func WithPhotosTransport(rt http.RoundTripper) PhotosOption {
	return func(p *PhotosProvider) { p.client.Transport = rt }
}

// selectAlbumWindow takes the provider lock and selects the next album window.
func (p *PhotosProvider) selectAlbumWindow(scraped []PhotoItem) []PhotoItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selectAlbumWindowLocked(scraped)
}

// Cache returns the coordinator's in-memory SWR cache.
func (c *ProviderCoordinator) Cache() *SWRCache {
	return c.cache
}

// Get returns the cached payload for the given widgetID, if present (test helper).
func (c *SWRCache) Get(widgetID string) (WidgetPayload, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.entries[widgetID]
	if !ok {
		return WidgetPayload{}, false
	}
	return e.toPayload(), true
}

// NewPhotosHTMLServer serves body as text/html at the server root.
var NewPhotosHTMLServer = newPhotosHTMLServer
