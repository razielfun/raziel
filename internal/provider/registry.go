package provider

import "fmt"

type Registry struct {
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return p, nil
}

func (r *Registry) Default() (Provider, error) {
	if len(r.providers) == 0 {
		return nil, fmt.Errorf("no providers registered")
	}
	// Prefer fly if present
	if p, ok := r.providers["fly"]; ok {
		return p, nil
	}
	for _, p := range r.providers {
		return p, nil
	}
	return nil, fmt.Errorf("no providers registered")
}
