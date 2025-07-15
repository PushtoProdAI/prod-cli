package workflowext

import (
	"context"
	"testing"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/stretchr/testify/assert"
)

const (
	key     = "testKey"
	testVal = "testVal"
)

func TestStringPropagator_ContextRoundTrip(t *testing.T) {
	prop := NewPropagator(key, StringSerializer{})

	ctx := context.Background()
	val, _ := prop.FromContext(ctx)
	assert.Equal(t, "", val)

	ctx = prop.WithContext(ctx, testVal)
	meta := &workflow.Metadata{}
	err := prop.Inject(ctx, meta)
	assert.NoError(t, err)
	assert.Equal(t, testVal, meta.Get(key))

	newCtx, err := prop.Extract(context.Background(), meta)
	assert.NoError(t, err)

	val, ok := prop.FromContext(newCtx)
	assert.True(t, ok)
	assert.Equal(t, testVal, val)
}

func TestStringPropagator_WorkflowRoundTrip(t *testing.T) {
	prop := NewPropagator(key, StringSerializer{})

	wfCtx := workflow.NewDisconnectedContext(nil)
	wfCtx = prop.WithWorkflowContext(wfCtx, testVal)

	meta := &workflow.Metadata{}
	err := prop.InjectFromWorkflow(wfCtx, meta)
	assert.NoError(t, err)
	assert.Equal(t, testVal, meta.Get(key))

	newWfCtx, err := prop.ExtractToWorkflow(workflow.NewDisconnectedContext(nil), meta)
	assert.NoError(t, err)

	val, ok := prop.FromWorkflowContext(newWfCtx)
	assert.True(t, ok)
	assert.Equal(t, testVal, val)
}
