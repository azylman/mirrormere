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
