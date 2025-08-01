package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"runtime"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/go-errors/errors"
	"github.com/manifoldco/promptui"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment"
)

type Agent struct {
	sm          deploySM
	wfClient    *client.Client
	interactive bool
	deployPlan  *deployPlan
	dryRun      bool
}

func NewAgent(wfClient *client.Client, interactive bool) *Agent {
	a := &Agent{wfClient: wfClient, interactive: interactive}
	sm := deploySM{currentState: a.plan}
	a.sm = sm
	return a
}

type deployPlan struct {
	Action           Action
	Platform         Platform
	Source           string
	Spec             analyzer.ProjectSpec
	Summary          string
	DryRunFromPrompt bool
}

//go:generate stringer -type=Platform,Action -output=types_string.go
type Platform int

const (
	Render Platform = iota
	FlyIO
	UnknownPlatform
)

type Action int

const (
	Deploy Action = iota
	UnknownAction
)

type (
	stateFn  func(ctx context.Context, input string, out io.Writer) (stateFn, error)
	deploySM struct {
		currentState stateFn
	}
)

func (sm *deploySM) next(ctx context.Context, input string, out io.Writer) error {
	if sm.currentState == nil {
		return nil
	}

	nextState, err := sm.currentState(ctx, input, out)
	if err != nil {
		log.Printf("Error in state %T: %v\n", sm.currentState, err)
		return err
	}

	sm.currentState = nextState
	return nil
}

func (a *Agent) SetDryRun(dryRun bool) {
	a.dryRun = dryRun
}

func (a *Agent) SetInteractive(interactive bool) {
	a.interactive = interactive
}

func (a *Agent) Process(ctx context.Context, input string, out io.Writer) {
	a.sm.next(ctx, input, out)
}

func (a *Agent) plan(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	wf, err := Workflows{}.PlanDeploy(ctx, a.wfClient, input)
	if err != nil {
		log.Printf("Workflow execution result: %v\n", err)
	}

	plan, err := client.GetWorkflowResult[deployPlan](ctx, a.wfClient, wf, 30*time.Second)
	if err != nil {
		fmt.Fprintf(out, "Error getting workflow result: %v\n", err)
	}

	// Check if dry-run was inferred from the prompt and merge with existing flag
	if plan.DryRunFromPrompt && !a.dryRun {
		a.dryRun = true
		fmt.Fprint(out, "🔍 Detected dry-run request from your prompt - simulating execution without making changes\n")
	}

	fmt.Fprintf(out, "%s\n", plan.Summary)
	fmt.Fprint(out, "-------\n")
	if a.dryRun {
		fmt.Fprint(out, "🔍 DRY RUN MODE - No changes will be made\n")
	}
	fmt.Fprintf(out, "Action: %s\n", plan.Action)
	fmt.Fprintf(out, "Platform: %s\n", plan.Platform)
	fmt.Fprintf(out, "Source: %s\n", plan.Source)
	fmt.Fprintf(out, "Name: %s\n", plan.Spec.Name)
	fmt.Fprintf(out, "Language: %s\n", plan.Spec.Language)
	fmt.Fprint(out, "-------\n")

	if !shouldProceed(plan) {
		fmt.Fprintf(out, "Cannot proceed with deployment plan\n")
		return a.plan, nil
	}
	a.deployPlan = &plan

	// Skip confirmation prompt in dry-run mode
	if a.dryRun {
		fmt.Fprintf(out, "Executing a dry-run deployment. Please wait as we calculate pricing and identify potential issues before you deploy...\n")
		return a.deploy(ctx, input, out)
	}

	if a.interactive {
		// automatically advance the next state, don't need to wait for input here
		return a.confirmWithPrompt(ctx, input, out)
	}
	return a.confirm, nil
}

func (a *Agent) confirmWithPrompt(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	prompt := promptui.Prompt{
		Label:     "Do you want to proceed with the deployment",
		IsConfirm: true,
	}

	result, err := prompt.Run()
	if err != nil {
		if err == promptui.ErrInterrupt {
			fmt.Fprintf(out, "Operation cancelled\n")
			return a.plan, nil
		}
		if err == promptui.ErrAbort {
			return a.plan, nil
		}
		return nil, errors.Errorf("prompt failed: %v", err)
	}

	if result == "y" || result == "yes" {
		fmt.Fprintf(out, "Proceeding with deployment...\n")
		return a.deploy(ctx, input, out)
	}

	fmt.Fprintf(out, "Deployment cancelled\n")
	return a.plan, nil
}

func (a *Agent) confirm(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	return a.deploy, nil
}

func (a *Agent) deploy(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	if a.dryRun {
		return a.dryRunDeploy(ctx, input, out)
	}

	wf, err := Workflows{}.Deploy(ctx, a.wfClient, *a.deployPlan)
	if err != nil {
		log.Printf("Workflow execution result: %v\n", err)
		fmt.Fprint(out, "Sorry, couldn't create a deployment plan \n")
		return a.plan, nil
	}

	// give a generous timeout for the deployment to complete
	url, err := client.GetWorkflowResult[string](ctx, a.wfClient, wf, 10*time.Minute)
	if err != nil {
		a.wfClient.CancelWorkflowInstance(ctx, wf)
		fmt.Fprint(out, "Sorry, we had trouble deploying your project \n")
		return a.plan, nil
	}

	io.WriteString(out, "Deployed!...🚀\n")
	if url != "" {
		fmt.Fprintf(out, "You can access your deployment at: %s\n", url)
		openInBrowser(url)
	}

	// In non-interactive mode, end the state machine
	if !a.interactive {
		return nil, nil
	}
	// In interactive mode, return to plan state for more commands
	return a.plan, nil
}

func (a *Agent) dryRunDeploy(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	wf, err := Workflows{}.DryRunDeploy(ctx, a.wfClient, *a.deployPlan)
	if err != nil {
		log.Printf("Dry-run workflow execution result: %v\n", err)
		fmt.Fprint(out, "Sorry, couldn't create a dry-run deployment plan \n")
		return a.plan, nil
	}

	// get the dry-run result with a shorter timeout since it's just planning
	result, err := client.GetWorkflowResult[DryRunResult](ctx, a.wfClient, wf, 2*time.Minute)
	if err != nil {
		a.wfClient.CancelWorkflowInstance(ctx, wf)
		fmt.Fprint(out, "Sorry that we had trouble creating the dry-run preview \n")
		return a.plan, nil
	}

	// Display the dry-run preview
	a.displayDryRunResult(out, result)

	// In non-interactive mode, end the state machine
	if !a.interactive {
		return nil, nil
	}
	// In interactive mode, return to plan state for more commands
	return a.plan, nil
}

func shouldProceed(plan deployPlan) bool {
	if plan.Action == UnknownAction {
		return false
	}
	if plan.Platform == UnknownPlatform {
		return false
	}
	if plan.Spec.Name == "" || plan.Spec.Language == "" {
		return false
	}
	return true
}

type DryRunResult struct {
	Steps            []DryRunStep            `json:"steps"`
	EstimatedCosts   deployment.CostEstimate `json:"estimatedCosts"`
	CredentialStatus map[string]bool         `json:"credentialStatus"`
	ConflictChecks   []ConflictCheck         `json:"conflictChecks"`
	ValidationErrors []string                `json:"validationErrors"`
}

type DryRunStep struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Type        string         `json:"type"`
	Config      map[string]any `json:"config"`
	DependsOn   []string       `json:"dependsOn"`
}

type ConflictCheck struct {
	Resource string `json:"resource"`
	Status   string `json:"status"` // "ok", "conflict", "warning"
	Message  string `json:"message"`
}

func (a *Agent) displayDryRunResult(out io.Writer, result DryRunResult) {
	fmt.Fprint(out, "\n🔍 DRY RUN PREVIEW\n")
	fmt.Fprint(out, "==================\n\n")

	// Show validation errors first
	if len(result.ValidationErrors) > 0 {
		fmt.Fprint(out, "❌ VALIDATION ERRORS:\n")
		for _, err := range result.ValidationErrors {
			fmt.Fprintf(out, "  • %s\n", err)
		}
		fmt.Fprint(out, "\n")
	}

	// Show credential status
	fmt.Fprint(out, "🔑 CREDENTIAL STATUS:\n")
	for service, valid := range result.CredentialStatus {
		status := "✅"
		if !valid {
			status = "❌"
		}
		fmt.Fprintf(out, "  %s %s\n", status, service)
	}
	fmt.Fprint(out, "\n")

	// Show conflict checks
	if len(result.ConflictChecks) > 0 {
		fmt.Fprint(out, "🔍 CONFLICT CHECKS:\n")
		for _, check := range result.ConflictChecks {
			var icon string
			switch check.Status {
			case "ok":
				icon = "✅"
			case "conflict":
				icon = "❌"
			case "warning":
				icon = "⚠️"
			default:
				icon = "❓"
			}
			fmt.Fprintf(out, "  %s %s: %s\n", icon, check.Resource, check.Message)
		}
		fmt.Fprint(out, "\n")
	}

	// Show planned actions
	fmt.Fprint(out, "📋 PLANNED ACTIONS:\n")
	for i, step := range result.Steps {
		fmt.Fprintf(out, "%d. %s\n", i+1, step.Description)
		fmt.Fprintf(out, "   Type: %s\n", step.Type)
		if len(step.DependsOn) > 0 {
			fmt.Fprintf(out, "   Depends on: %v\n", step.DependsOn)
		}
	}
	fmt.Fprint(out, "\n")

	// Show estimated costs
	if result.EstimatedCosts.Total > 0 {
		fmt.Fprint(out, "💰 ESTIMATED MONTHLY COSTS:\n")
		for _, service := range result.EstimatedCosts.Services {
			var description string
			if service.Plan != "" {
				if service.Storage > 0 {
					description = fmt.Sprintf("%s (%s, %dGB storage)", service.Provider, service.Plan, service.Storage)
				} else {
					description = fmt.Sprintf("%s (%s)", service.Provider, service.Plan)
				}
			} else {
				description = service.Provider
			}
			fmt.Fprintf(out, "  • %s: $%.2f\n", description, service.Cost)
		}
		fmt.Fprintf(out, "  Total: $%.2f/month\n", result.EstimatedCosts.Total)
		fmt.Fprint(out, "\n")
	}
}

func openInBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}
