package agent

// FrameworkRegistry manages framework handlers for all languages
type FrameworkRegistry struct {
	handlers map[string]FrameworkHandler
}

// NewFrameworkRegistry creates a new registry with all built-in framework handlers
func NewFrameworkRegistry() *FrameworkRegistry {
	registry := &FrameworkRegistry{
		handlers: make(map[string]FrameworkHandler),
	}

	// Register JavaScript framework handlers
	registry.RegisterHandler(&RemixHandler{})
	registry.RegisterHandler(&SvelteKitHandler{})
	registry.RegisterHandler(&NuxtHandler{})
	registry.RegisterHandler(&TanStackStartHandler{})

	// Register Python framework handlers
	registry.RegisterHandler(&DjangoHandler{})

	return registry
}

// RegisterHandler adds a framework handler to the registry
func (r *FrameworkRegistry) RegisterHandler(handler FrameworkHandler) {
	r.handlers[handler.GetName()] = handler
}

// GetHandler returns the handler for a framework, or nil if not found
func (r *FrameworkRegistry) GetHandler(framework string) FrameworkHandler {
	return r.handlers[framework]
}

// Global framework registry instance
var frameworkRegistry = NewFrameworkRegistry()
