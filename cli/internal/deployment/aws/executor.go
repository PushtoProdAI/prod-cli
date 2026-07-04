package aws

import (
	"io"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

type StepExecutor = deployment.StepExecutor[AWSClient]

func NewStepExecutor(client AWSClient, writer io.Writer) *StepExecutor {
	return deployment.NewStepExecutor(client, writer)
}
