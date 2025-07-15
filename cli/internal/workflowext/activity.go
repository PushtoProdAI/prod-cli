package workflowext

import (
	"github.com/cschleiden/go-workflows/registry"
	"github.com/go-errors/errors"
)

type Activity struct {
	ActFunc any
	Name    string
}

func (a *Activity) Register(reg Registry) error {
	if err := reg.RegisterActivity(a.ActFunc, registry.WithName(a.Name)); err != nil {
		return errors.Errorf("failed to register activity %q: %w", a.Name, err)
	}

	return nil
}
