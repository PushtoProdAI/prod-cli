package agent

import (
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/llm"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	AgentDetermineIntent            = "agent.determineIntent"
	AgentAnalyzeProject             = "agent.analyzeProject"
	AgentSummarize                  = "agent.summarize"
	AgentGetRenderWorkspace         = "agent.getRenderWorkspace"
	AgentDeploySteps                = "agent.deploySteps"
	AgentSummarizeDeploySteps       = "agent.summarizeDeploySteps"
	AgentSummarizeError             = "agent.summarizeError"
	AgentEstimateRenderCosts        = "agent.estimateRenderCosts"
	AgentEstimateFlyioCosts         = "agent.estimateFlyioCosts"
	AgentEstimateNetlifyCosts       = "agent.estimateNetlifyCosts"
	AgentEstimateVercelCosts        = "agent.estimateVercelCosts"
	AgentEstimateHerokuCosts        = "agent.estimateHerokuCosts"
	AgentEstimateAWSCosts           = "agent.estimateAWSCosts"
	AgentGetRenderServiceURL        = "agent.getRenderServiceURL"
	AgentWaitForRenderDeploy        = "agent.waitForRenderDeploy"
	AgentWaitForAWSStack            = "agent.waitForAWSStack"
	AgentUpdateAWSStack             = "agent.updateAWSStack"
	AgentIsURLLive                  = "agent.isURLLive"
	AgentSendProjectStats           = "agent.sendProjectStats"
	AgentGetFlyIOAppURL             = "agent.getFlyIOAppURL"
	AgentCategorizeEnvVars          = "agent.categorizeEnvVars"
	AgentReadEnvFiles               = "agent.readEnvFiles"
	AgentCreateDockerRepo           = "agent.createDockerRepo"
	AgentDetermineRootPath          = "agent.determineRootPath"
	AgentDetermineBuildOutput       = "agent.determineBuildOutput"
	AgentDetermineRunCommand        = "agent.determineRunCommand"
	AgentDetermineMigrationCommand  = "agent.determineMigrationCommand"
	AgentCreatePackageLock          = "agent.createPackageLock"
	AgentUpdateJavaScriptConfig     = "agent.updateJavaScriptConfig"
	AgentGeneratePythonVersion      = "agent.generatePythonVersion"
	AgentConfigurePythonFramework   = "agent.configurePythonFramework"
	AgentSetupPythonServer          = "agent.setupPythonServer"
	AgentConfigurePythonStaticFiles = "agent.configurePythonStaticFiles"
	AgentRestoreConfigFromBackup    = "agent.restoreConfigFromBackup"
	AgentPrepareDeployment          = "agent.prepareDeployment"
	AgentCheckExistingProject       = "agent.checkExistingProject"
	AgentDetectPlatformsForRollback = "agent.detectPlatformsForRollback"
	AgentDetectProject              = "agent.detectProject"
	AgentBuildDetectionSummary      = "agent.buildDetectionSummary"
	AgentLogDeploymentStart         = "agent.logDeploymentStart"
	AgentUpdateDeploymentStatus     = "agent.updateDeploymentStatus"
	AgentRollbackDeployment         = "agent.rollbackDeployment"
	AgentGetPreviousDeployment      = "agent.getPreviousDeployment"
	AgentRunECSMigration            = "agent.runECSMigration"
)

type Activities struct {
	renderClient render.RenderClient
	flyClient    flyio.FlyioClient
	beClient     *backend.Client
	uiWriter     output.StatusWriter
	llmClient    llm.Client
}

func (a *Activities) Activities() []workflowext.Activity {
	return []workflowext.Activity{
		{Name: AgentDetermineIntent, ActFunc: a.determineIntent},
		{Name: AgentAnalyzeProject, ActFunc: a.analyze},
		{Name: AgentSummarize, ActFunc: a.summarize},
		{Name: AgentGetRenderWorkspace, ActFunc: a.getRenderWorkspace},
		{Name: AgentSummarizeDeploySteps, ActFunc: a.summarizeDeploySteps},
		{Name: AgentDeploySteps, ActFunc: a.deploySteps},
		{Name: AgentSummarizeError, ActFunc: a.summarizeError},
		{Name: AgentEstimateRenderCosts, ActFunc: a.estimateRenderCosts},
		{Name: AgentEstimateFlyioCosts, ActFunc: a.estimateFlyioCosts},
		{Name: AgentEstimateNetlifyCosts, ActFunc: a.estimateNetlifyCosts},
		{Name: AgentEstimateVercelCosts, ActFunc: a.estimateVercelCosts},
		{Name: AgentEstimateHerokuCosts, ActFunc: a.estimateHerokuCosts},
		{Name: AgentEstimateAWSCosts, ActFunc: a.estimateAWSCosts},
		{Name: AgentGetRenderServiceURL, ActFunc: a.getRenderServiceURL},
		{Name: AgentWaitForRenderDeploy, ActFunc: a.waitForRenderDeploy},
		{Name: AgentWaitForAWSStack, ActFunc: a.waitForAWSStack},
		{Name: AgentIsURLLive, ActFunc: a.isURLLive},
		{Name: AgentSendProjectStats, ActFunc: a.sendProjectStats},
		{Name: AgentGetFlyIOAppURL, ActFunc: a.getFlyIOAppURL},
		{Name: AgentCategorizeEnvVars, ActFunc: a.categorizeEnvVarsForDeployment},
		{Name: AgentReadEnvFiles, ActFunc: a.getEnvVarsFromEnvFiles},
		{Name: AgentCreateDockerRepo, ActFunc: a.createDockerRepo},
		{Name: AgentDetermineRootPath, ActFunc: a.determineRootPath},
		{Name: AgentDetermineBuildOutput, ActFunc: a.determineBuildOutput},
		{Name: AgentDetermineRunCommand, ActFunc: a.determineRunCommand},
		{Name: AgentDetermineMigrationCommand, ActFunc: a.determineMigrationCommand},
		{Name: AgentCreatePackageLock, ActFunc: a.createPackageLockJSON},
		{Name: AgentUpdateJavaScriptConfig, ActFunc: a.updateJavaScriptConfig},
		{Name: AgentGeneratePythonVersion, ActFunc: a.generatePythonVersion},
		{Name: AgentConfigurePythonFramework, ActFunc: a.configurePythonFramework},
		{Name: AgentSetupPythonServer, ActFunc: a.setupPythonServer},
		{Name: AgentConfigurePythonStaticFiles, ActFunc: a.configureDjangoStaticFiles},
		{Name: AgentRestoreConfigFromBackup, ActFunc: a.restoreFromBackup},
		{Name: AgentPrepareDeployment, ActFunc: a.prepareDeployment},
		{Name: AgentCheckExistingProject, ActFunc: a.checkExistingProject},
		{Name: AgentDetectPlatformsForRollback, ActFunc: a.detectPlatformsForRollback},
		{Name: AgentLogDeploymentStart, ActFunc: a.logDeploymentStart},
		{Name: AgentUpdateDeploymentStatus, ActFunc: a.updateDeploymentStatus},
		{Name: AgentGetPreviousDeployment, ActFunc: a.getPreviousDeployment},
		{Name: AgentRollbackDeployment, ActFunc: a.rollbackDeployment},
		{Name: AgentRunECSMigration, ActFunc: a.runECSMigration},
		{Name: AgentUpdateAWSStack, ActFunc: a.updateAWSStack},
	}
}
