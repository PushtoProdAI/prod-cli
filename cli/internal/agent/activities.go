package agent

import (
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	AgentDetermineIntent      = "agent.determineIntent"
	AgentAnalyzeProject       = "agent.analyzeProject"
	AgentSummarizeIntent      = "agent.summarize"
	AgentGetRenderWorkspace   = "agent.getRenderWorkspace"
	AgentDeploySteps          = "agent.deploySteps"
	AgentSummarizeDeploySteps = "agent.summarizeDeploySteps"
	AgentSummarizeError       = "agent.summarizeError"
	AgentEstimateRenderCosts  = "agent.estimateRenderCosts"
	AgentGetRenderServiceURL  = "agent.getRenderServiceURL"
	AgentIsURLLive            = "agent.isURLLive"
	AgentSendProjectStats     = "agent.sendProjectStats"
	AgentGetFlyIOAppURL       = "agent.getFlyIOAppURL"
)

type Activities struct {
	renderClient render.RenderClient
	flyClient    flyio.FlyioClient
	beClient     *backend.Client
	uiWriter     output.StatusWriter
}

func (a *Activities) Activities() []workflowext.Activity {
	return []workflowext.Activity{
		{Name: AgentDetermineIntent, ActFunc: a.determineIntent},
		{Name: AgentAnalyzeProject, ActFunc: a.analyze},
		{Name: AgentSummarizeIntent, ActFunc: a.summarize},
		{Name: AgentGetRenderWorkspace, ActFunc: a.getRenderWorkspace},
		{Name: AgentSummarizeDeploySteps, ActFunc: a.summarizeDeploySteps},
		{Name: AgentDeploySteps, ActFunc: a.deploySteps},
		{Name: AgentSummarizeError, ActFunc: a.summarizeError},
		{Name: AgentEstimateRenderCosts, ActFunc: a.estimateRenderCosts},
		{Name: AgentGetRenderServiceURL, ActFunc: a.getRenderServiceURL},
		{Name: AgentIsURLLive, ActFunc: a.isURLLive},
		{Name: AgentSendProjectStats, ActFunc: a.sendProjectStats},
		{Name: AgentGetFlyIOAppURL, ActFunc: a.getFlyIOAppURL},
	}
}
