package workflowext

import (
	"context"
	"encoding/json"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
)

type ValueSerializer[T any] interface {
	Serialize(T) (string, error)
	Deserialize(string) (T, error)
}

type JSONSerializer[T any] struct{}

func (JSONSerializer[T]) Serialize(v T) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

//nolint:ireturn //returning generic here is what we want
func (JSONSerializer[T]) Deserialize(s string) (T, error) {
	var v T
	err := json.Unmarshal([]byte(s), &v)
	if err != nil {
		return v, errors.Errorf("error unmarshaling context value: %w", err)
	}
	return v, nil
}

type StringSerializer struct{}

func (StringSerializer) Serialize(v string) (string, error) {
	return v, nil
}

func (StringSerializer) Deserialize(s string) (string, error) {
	return s, nil
}

type Propagator[T any] struct {
	metaKey    string
	serializer ValueSerializer[T]
}

func NewPropagator[T any](metaKey string, serializer ValueSerializer[T]) *Propagator[T] {
	return &Propagator[T]{metaKey: metaKey, serializer: serializer}
}

func (p *Propagator[T]) WithContext(ctx context.Context, val T) context.Context {
	return context.WithValue(ctx, p, val)
}

//nolint:ireturn //returning generic here is what we want
func (p *Propagator[T]) FromContext(ctx context.Context) (T, bool) {
	v, ok := ctx.Value(p).(T)
	return v, ok
}

func (p *Propagator[T]) WithWorkflowContext(ctx workflow.Context, val T) workflow.Context {
	return workflow.WithValue(ctx, p, val)
}

//nolint:ireturn //returning generic here is what we want
func (p *Propagator[T]) FromWorkflowContext(ctx workflow.Context) (T, bool) {
	v, ok := ctx.Value(p).(T)
	return v, ok
}

func (p *Propagator[T]) Extract(ctx context.Context, meta *workflow.Metadata) (context.Context, error) {
	valStr := meta.Get(p.metaKey)
	if valStr == "" {
		return ctx, nil
	}
	val, err := p.serializer.Deserialize(valStr)
	if err != nil {
		return nil, errors.Errorf("failed to deserialize: %w", err)
	}
	return context.WithValue(ctx, p, val), nil
}

func (p *Propagator[T]) ExtractToWorkflow(ctx workflow.Context, meta *workflow.Metadata) (workflow.Context, error) {
	valStr := meta.Get(p.metaKey)
	if valStr == "" {
		return ctx, nil
	}
	val, err := p.serializer.Deserialize(valStr)
	if err != nil {
		return nil, errors.Errorf("failed to deserialize: %w", err)
	}
	return workflow.WithValue(ctx, p, val), nil
}

func (p *Propagator[T]) Inject(ctx context.Context, meta *workflow.Metadata) error {
	val, ok := p.FromContext(ctx)
	if !ok {
		return nil
	}
	str, err := p.serializer.Serialize(val)
	if err != nil {
		return errors.Errorf("failed to serialize: %w", err)
	}
	meta.Set(p.metaKey, str)
	return nil
}

func (p *Propagator[T]) InjectFromWorkflow(ctx workflow.Context, meta *workflow.Metadata) error {
	val, ok := p.FromWorkflowContext(ctx)
	if !ok {
		return nil
	}
	str, err := p.serializer.Serialize(val)
	if err != nil {
		return errors.Errorf("failed to serialize: %w", err)
	}
	meta.Set(p.metaKey, str)
	return nil
}
