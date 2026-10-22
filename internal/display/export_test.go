package display

func WithTemplatePath(path string) HandlerOption { return func(h *Handler) { h.templatePath = path } }

func WithLocalTemplatePath(path string) HandlerOption {
	return func(h *Handler) { h.localTemplatePath = path }
}
func WithStaticDir(dir string) HandlerOption      { return func(h *Handler) { h.staticDir = dir } }
func WithLocalStaticDir(dir string) HandlerOption { return func(h *Handler) { h.localStaticDir = dir } }

func WithTemplateContent(content []byte) HandlerOption {
	return func(h *Handler) { h.templateContent = content }
}
func WithEmbeddedTemplate(content []byte) HandlerOption { return WithTemplateContent(content) }
func WithTimezone(tz string) HandlerOption              { return func(h *Handler) { h.timezone = tz } }

func WithHideCursor(hide bool) HandlerOption { return func(h *Handler) { h.hideCursor = hide } }

func WithRemoteURL(url string) HandlerOption { return func(h *Handler) { h.remoteURL = url } }

func WithHeaderVideoTemplateData(d *HeaderVideoTemplateData) HandlerOption {
	return func(h *Handler) { h.headerVideo = d }
}

func (h *Handler) SetTimezone(tz string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timezone = tz
}

func (h *Handler) SetHideCursor(hide bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hideCursor = hide
}

func (h *Handler) SetRemoteURL(url string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.remoteURL = url
}
