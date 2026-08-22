package check

type Registry struct {
	checks []Check
}

func NewRegistry(checks ...Check) *Registry { return &Registry{checks: checks} }

func (r *Registry) All() []Check { return r.checks }

func (r *Registry) Get(name string) (Check, bool) {
	for _, c := range r.checks {
		if c.Name() == name {
			return c, true
		}
	}
	return nil, false
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.checks))
	for _, c := range r.checks {
		names = append(names, c.Name())
	}
	return names
}
