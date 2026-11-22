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

// NewPhotosHTMLServer serves body as text/html at the server root.
var NewPhotosHTMLServer = newPhotosHTMLServer
