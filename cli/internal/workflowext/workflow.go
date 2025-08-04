package workflowext

import (
	"github.com/cschleiden/go-workflows/registry"
	"github.com/go-errors/errors"
)

type Workflow struct {
	WorkflowFunc any
	Name         string
}

func (w *Workflow) Register(reg Registry) error {
	if err := reg.RegisterWorkflow(w.WorkflowFunc, registry.WithName(w.Name)); err != nil {
		return errors.Errorf("failed to register workflow %q: %w", w.Name, err)
	}

	return nil
}
