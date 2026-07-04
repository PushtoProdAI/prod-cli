package render

import (
	"io"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

type StepExecutor = deployment.StepExecutor[RenderClient]

func NewStepExecutor(client RenderClient, writer io.Writer) *StepExecutor {
	return deployment.NewStepExecutor(client, writer)
}
