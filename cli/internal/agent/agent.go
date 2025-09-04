package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/auth"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/output"
)

type TUIWriter interface {
	io.Writer
	SendConfirmation(message string, callback func(bool))
	SendAPIKeyPrompt(message string)
	SendSelect(message string, options []string)
	SendTextPrompt(message string)
	SendTextPromptWithDefault(message string, defaultValue string)
	SendPlan(plan DeployPlan, dryRun bool)
	StopSpinner()
}

type EnvVarWithStatus struct {
	deployment.EnvVar
	Status string // "pending", "collected", "db_related"
}

type Agent struct {
	sm           deploySM
	wfClient     *client.Client
	interactive  bool
	DeployPlan   *DeployPlan
	dryRun       bool
	UIOutput     io.Writer
	auth         auth.AuthProvider
	envVars      []EnvVarWithStatus
	internalAuth *auth.SupabaseAuth
}

type agentContextKey string

const agentAuthSession agentContextKey = "AuthSession"

func NewAgent(wfClient *client.Client, internalAuth *auth.SupabaseAuth, interactive bool) *Agent {
	a := &Agent{
		wfClient:     wfClient,
		interactive:  interactive,
		envVars:      make([]EnvVarWithStatus, 0),
		internalAuth: internalAuth,
	}
	sm := deploySM{currentState: a.plan}
	a.sm = sm
	return a
}

type DeployPlan struct {
	Action           Action
	Platform         Platform
	Source           string
	Spec             analyzer.ProjectSpec
	Summary          string
	DryRunFromPrompt bool
	CollectedEnvVars []deployment.EnvVar
	Pricing          deployment.CostEstimate
}

type deployResult struct {
	Url   string
	Error deployError
}

type deployError struct {
	Summary      string
	Remediations []remediation
}

type remediation struct {
	Description string
	CliCommand  string
}

//go:generate stringer -type=Platform,Action -output=types_string.go
type Platform int

const (
	Render Platform = iota
	FlyIO
	Netlify
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

// Helper methods for TUI operations - direct TUI calls
func (a *Agent) sendPlan(out io.Writer, plan DeployPlan, dryRun bool) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.SendPlan(plan, dryRun)
}

func (a *Agent) sendConfirmation(out io.Writer, message string) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.SendConfirmation(message, nil)
}

func (a *Agent) sendSelect(out io.Writer, message string, options []string) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.SendSelect(message, options)
}

func (a *Agent) sendAPIKeyPrompt(out io.Writer, message string) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.SendAPIKeyPrompt(message)
}

func (a *Agent) sendTextPrompt(out io.Writer, message string) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.SendTextPrompt(message)
}

func (a *Agent) sendTextPromptWithDefault(out io.Writer, message string, defaultValue string) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.SendTextPromptWithDefault(message, defaultValue)
}

func (a *Agent) stopSpinner(out io.Writer) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.StopSpinner()
}

func (a *Agent) Process(ctx context.Context, input string, out io.Writer) {
	log.Printf("Processing input: %s\n", input)
	output := out
	if a.UIOutput != nil {
		output = a.UIOutput
	}
	// handle auth before processing the input
	if !a.internalAuth.IsAuthenticated() {
		fmt.Fprint(output, "🔐 Before we proceed, let's get you logged in!\n")
		authenticated := a.authenticateCLI(ctx)
		if !authenticated {
			fmt.Fprint(output, "❌ Authentication failed. Please try again.\n")
			// don't proceed to the next state if auth failed
			return
		}
	}
	session, err := a.internalAuth.GetSession()
	if err != nil {
		slog.Error("Failed to get session", "error", err)
	}

	a.sm.next(WithCtxSession(ctx, session), input, output)
}

func (a *Agent) plan(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	wf, err := Workflows{}.PlanDeploy(ctx, a.wfClient, input)
	if err != nil {
		log.Printf("Workflow execution result: %v\n", err)
	}

	plan, err := client.GetWorkflowResult[DeployPlan](ctx, a.wfClient, wf, 30*time.Second)
	if err != nil {
		fmt.Fprintf(out, "Error getting workflow result: %v\n", err)
	}

	// Check if dry-run was inferred from the prompt and merge with existing flag
	if plan.DryRunFromPrompt && !a.dryRun {
		a.dryRun = true
		fmt.Fprint(out, "🔍 Detected dry-run request from your prompt - simulating execution without making changes\n")
	}

	a.sendPlan(out, plan, a.dryRun)

	if !shouldProceed(plan) {
		fmt.Fprintf(out, "Cannot proceed with deployment plan\n")
		return a.plan, nil
	}
	a.DeployPlan = &plan

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
	// Check if this is the initial call or a response to confirmation
	if input == "" {
		// Initial call - send confirmation prompt
		a.sendConfirmation(out, "Do you want to proceed with the deployment?")
		return a.waitForConfirmation, nil
	}
	// This is a response to confirmation - process it
	return a.processConfirmationResponse(ctx, input, out)
}

func (a *Agent) waitForConfirmation(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	// This state waits for user input during confirmation
	return a.processConfirmationResponse(ctx, input, out)
}

func (a *Agent) processConfirmationResponse(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	input = strings.ToLower(strings.TrimSpace(input))

	if input == "y" || input == "yes" {
		fmt.Fprintf(out, "Proceeding with deployment...\n")
		return a.categorizeEnvironmentVariables(ctx, input, out)
	}

	if input == "n" || input == "no" {
		fmt.Fprintf(out, "Deployment cancelled\n")
		return a.plan, nil
	}

	// Invalid response - ask again
	a.sendConfirmation(out, "Do you want to proceed with the deployment?")
	return a.waitForConfirmation, nil
}

func (a *Agent) confirm(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	return a.deploy, nil
}

func (a *Agent) categorizeEnvironmentVariables(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprintf(out, "🔍 Categorizing environment variables...\n")

	wf, err := Workflows{}.CategorizeEnvVars(ctx, a.wfClient, *a.DeployPlan)
	if err != nil {
		fmt.Fprintf(out, "❌ Error categorizing environment variables: %v\n", err)
		return a.deploy(ctx, input, out)
	}

	envVars, err := client.GetWorkflowResult[[]deployment.EnvVar](ctx, a.wfClient, wf, 5*time.Minute)
	if err != nil {
		fmt.Fprintf(out, "❌ Error getting categorization result: %v\n", err)
		return a.deploy(ctx, input, out)
	}

	fmt.Fprintf(out, "✅ Environment variables categorized\n")

	// always initialize envVars slice to reset between deploys
	a.envVars = make([]EnvVarWithStatus, 0)

	// Process all environment variables and set their status
	var pendingCount int
	for _, envVar := range envVars {
		if envVar.IsNotDBRelated() {
			// This non-DB var needs user input
			a.envVars = append(a.envVars, EnvVarWithStatus{
				EnvVar: envVar,
				Status: "pending",
			})
			pendingCount++
		} else {
			// DB-related vars - deployment system will handle values
			a.envVars = append(a.envVars, EnvVarWithStatus{
				EnvVar: envVar,
				Status: "db_related",
			})
		}
	}

	if pendingCount > 0 {
		fmt.Fprintf(out, "Found %d environment variables that need values:\n", pendingCount)
		for _, envVar := range a.envVars {
			if envVar.Status == "pending" {
				fmt.Fprintf(out, "  - %s\n", envVar.Name)
			}
		}
		fmt.Fprint(out, "\n")
		fmt.Fprintf(out, "🔒 We'll display the values you enter in plaintext, but don't worry they are handled securely when we deploy!\n")
		return a.promptForEnvVarValue(ctx, input, out)
	}

	// All env vars are database-related or already have values, proceed with deployment
	fmt.Fprintf(out, "✅ All environment variables are ready. Proceeding with deployment...\n")
	return a.deploy(ctx, input, out)
}

func (a *Agent) promptForEnvVarValue(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	var currentEnvVar *EnvVarWithStatus

	for i := range a.envVars {
		if a.envVars[i].Status == "pending" {
			currentEnvVar = &a.envVars[i]
			break
		}
	}

	if currentEnvVar == nil {
		// No more pending env vars, proceed with deployment
		fmt.Fprintf(out, "All environment variable values collected. Proceeding with deployment...\n")
		return a.deploy(ctx, input, out)
	}

	promptMessage := fmt.Sprintf("Enter value for environment variable '%s':", currentEnvVar.Name)
	if currentEnvVar.Value != "" {
		// Use the enhanced method that pre-fills the input with the default value
		a.sendTextPromptWithDefault(out, promptMessage, currentEnvVar.Value)
	} else {
		a.sendTextPrompt(out, promptMessage)
	}
	return a.waitForEnvVarValue, nil
}

func (a *Agent) waitForEnvVarValue(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	userInput := strings.TrimSpace(input)

	for i := range a.envVars {
		if a.envVars[i].Status == "pending" {
			var finalValue string
			if userInput == "" && a.envVars[i].Value != "" {
				// User pressed Enter without input - use default from .env file
				finalValue = a.envVars[i].Value
				fmt.Fprintf(out, "✅ Using default value: %s = %s\n", a.envVars[i].Name, finalValue)
			} else if userInput != "" {
				// User provided a value - use it
				finalValue = userInput
				fmt.Fprintf(out, "✅ Set %s = %s\n", a.envVars[i].Name, finalValue)
			} else {
				// No user input and no default - keep empty (shouldn't happen for not_db_related)
				finalValue = ""
				fmt.Fprintf(out, "✅ Set %s = (empty)\n", a.envVars[i].Name)
			}

			// Update the env var with the final value and mark as collected
			a.envVars[i].Value = finalValue
			a.envVars[i].Status = "collected"
			break
		}
	}

	// Continue to next env var (no counter needed - we just find the next pending one)
	return a.promptForEnvVarValue(ctx, input, out)
}

func (a *Agent) deploy(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	if a.dryRun {
		return a.dryRunDeploy(ctx, input, out)
	}

	// Check authentication before deployment
	if a.DeployPlan.Platform == Render || a.DeployPlan.Platform == FlyIO {
		return a.checkAuthentication(ctx, input, out)
	}

	return a.executeDeployment(ctx, input, out)
}

func (a *Agent) executeDeployment(ctx context.Context, _ string, out io.Writer) (stateFn, error) {
	// Add collected environment variables to the deploy plan
	DeployPlanWithEnvVars := *a.DeployPlan

	// Convert EnvVarWithStatus back to deployment.EnvVar for deployment
	var collectedEnvVars []deployment.EnvVar
	for _, envVar := range a.envVars {
		if envVar.Status == "collected" || envVar.Status == "db_related" {
			collectedEnvVars = append(collectedEnvVars, envVar.EnvVar)
		}
	}
	DeployPlanWithEnvVars.CollectedEnvVars = collectedEnvVars

	wf, err := Workflows{}.Deploy(ctx, a.wfClient, DeployPlanWithEnvVars)
	if err != nil {
		log.Printf("Workflow execution result: %v\n", err)
		fmt.Fprint(out, "Sorry, couldn't create a deployment plan \n")
		return a.plan, nil
	}

	// give a generous timeout for the deployment to complete
	result, err := client.GetWorkflowResult[deployResult](ctx, a.wfClient, wf, 20*time.Minute)
	// manually stop the spinner in case anything is dangling from the deploy workflow
	a.stopSpinner(out)

	if err != nil {
		log.Printf("Deployment workflow execution result: %v\n", err)
		a.wfClient.CancelWorkflowInstance(ctx, wf)
		fmt.Fprint(out, "Sorry, we had trouble deploying your project \n")
		return a.plan, nil
	}

	if result.Error.Summary != "" {
		fmt.Fprint(out, "Sorry, we had trouble deploying your project \n")
		fmt.Fprintf(out, "%s\n", result.Error.Summary)
		if len(result.Error.Remediations) > 0 {
			fmt.Fprint(out, "Here are some suggestions to fix the issues:\n")
			for _, r := range result.Error.Remediations {
				fmt.Fprintf(out, " • %s\n", r.Description)
				if r.CliCommand != "" {
					fmt.Fprintf(out, "   Run: %s\n", r.CliCommand)
				}
			}
			fmt.Fprint(out, "Once you are ready to retry, just let me know!\n")
			// jump to the confirm state so that we give the user a chance to fix the issues and retry, without having to replan.
			return a.confirmWithPrompt(ctx, "", out)

		}
		if !a.interactive {
			return nil, nil
		}
		return a.plan, nil
	}

	io.WriteString(out, "Deployed!...🚀\n")
	if result.Url != "" {
		fmt.Fprintf(out, "You can access your deployment at: %s\n", result.Url)
		openInBrowser(result.Url)
	}

	// In non-interactive mode, end the state machine
	if !a.interactive {
		return nil, nil
	}
	// In interactive mode, return to plan state for more commands
	return a.plan, nil
}

func (a *Agent) dryRunDeploy(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	// Check authentication before dry run deployment
	if a.DeployPlan.Platform == Render {
		return a.checkRenderAuthenticationForDryRun(ctx, input, out)
	}

	return a.executeDryRun(ctx, input, out)
}

func shouldProceed(plan DeployPlan) bool {
	if plan.Action == UnknownAction {
		log.Printf("Validation failed: Action is UnknownAction")
		return false
	}
	if plan.Platform == UnknownPlatform {
		log.Printf("Validation failed: Platform is UnknownPlatform")
		return false
	}
	if plan.Spec.Name == "" || plan.Spec.Language == "" {
		log.Printf("Validation failed: Spec.Name or Spec.Language is empty")
		return false
	}

	log.Printf("Validation passed: All checks successful")
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

func (a *Agent) checkAuthentication(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprint(out, "Checking authentication...\n")

	authProvider, err := a.getAuthProvider(out)
	if err != nil {
		fmt.Fprintf(out, "Error getting authentication provider: %v\n", err)
	}

	// Check if already authenticated
	authenticated, err := authProvider.CheckAuthentication(ctx)
	if err != nil {
		fmt.Fprintf(out, "Error checking authentication: %v\n", err)
		return a.plan, err
	}

	if !authenticated {
		fmt.Fprintf(out, "🔐 Authentication required for %s deployment\n\n", a.DeployPlan.Platform)

		// Store the render auth for use in authentication states
		a.auth = authProvider

		// In non-interactive mode, if we are not authenticated exit state machine
		if !a.interactive {
			return nil, nil
		}

		// In interactive mode, transition to auth selection state
		a.sendSelect(out, "Choose authentication method:", []string{
			"Interactive login (recommended)",
			"Enter API key directly",
		})
		// Transition to waiting for auth selection
		return a.waitForAuthSelection, nil
	}

	// Already authenticated, proceed with deployment
	return a.executeDeployment(ctx, input, out)
}

func (a *Agent) getAuthProvider(out io.Writer) (auth.AuthProvider, error) {
	switch a.DeployPlan.Platform {
	case Render:
		apiKey := os.Getenv("RENDER_API_KEY")
		renderClient := render.NewHTTPRenderClient(apiKey, output.NewNoOpWriter())
		renderAuth := auth.NewRenderAuth(renderClient, out)
		return renderAuth, nil
	case FlyIO:
		return auth.NewFlyAuth(out), nil
	case Netlify:
		// TODO: Implement Netlify authentication
		return nil, fmt.Errorf("Netlify authentication not yet implemented")
	default:
		return nil, fmt.Errorf("unsupported platform: %s", a.DeployPlan.Platform)
	}
}

func (a *Agent) waitForAuthSelection(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	input = strings.TrimSpace(input)

	switch input {
	case "0": // First option - Interactive login
		return a.performOAuthLogin(ctx, input, out)
	case "1": // Second option - API key
		return a.promptForAPIKey(ctx, input, out)
	default:
		// Invalid selection, ask again
		a.sendSelect(out, "Choose authentication method:", []string{
			"Interactive login (recommended)",
			"Enter API key directly",
		})
		return a.waitForAuthSelection, nil
	}
}

func (a *Agent) promptForAPIKey(_ context.Context, _ string, out io.Writer) (stateFn, error) {
	// Send API key prompt
	a.sendAPIKeyPrompt(out, a.auth.APIKeyPrompt())
	return a.waitForAPIKey, nil
}

func (a *Agent) waitForAPIKey(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	apiKey := strings.TrimSpace(input)

	// Validate the API key by making a test call
	fmt.Fprint(out, "🔍 Validating API key...\n")
	valid, err := a.auth.ValidateAPIKey(ctx, apiKey)
	if err != nil {
		fmt.Fprintf(out, "❌ Failed to validate API key: %v\n", err)
		return a.promptForAPIKey(ctx, "", out)
	}

	if !valid {
		fmt.Fprint(out, "❌ Invalid API key - please check your key and try again\n")
		return a.promptForAPIKey(ctx, "", out)
	}

	fmt.Fprint(out, "✅ API key validated successfully!\n")
	fmt.Fprint(out, "💡 API key will only be available for this session.\n")

	// Continue with deployment
	return a.executeDeployment(ctx, input, out)
}

func (a *Agent) performOAuthLogin(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprint(out, "🚀 Starting authentication flow...\n")

	// Perform OAuth login using the auth package
	if err := a.auth.PerformOAuthLogin(ctx); err != nil {
		fmt.Fprintf(out, "❌ Authentication failed: %v\n", err)
		fmt.Fprint(out, "🔧 You can try option 2 (Manual API key setup) instead\n")
		return a.waitForAuthSelection, nil
	}

	fmt.Fprint(out, "✅ Authentication successful!\n")

	// Continue with deployment
	return a.executeDeployment(ctx, input, out)
}

func (a *Agent) checkRenderAuthenticationForDryRun(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprint(out, "Checking Render authentication...\n")

	// Get the render client from the workflow
	apiKey := os.Getenv("RENDER_API_KEY")
	renderClient := render.NewHTTPRenderClient(apiKey, output.NewNoOpWriter())
	renderAuth := auth.NewRenderAuth(renderClient, out)

	// Check if already authenticated
	authenticated, err := renderAuth.CheckAuthentication(ctx)
	if err != nil {
		fmt.Fprintf(out, "Error checking authentication: %v\n", err)
		return a.plan, err
	}

	if !authenticated {
		fmt.Fprint(out, "🔐 Authentication required for Render deployment\n\n")

		// Store the render auth for use in authentication states
		a.auth = renderAuth

		// In non-interactive mode, default to API key mode
		if !a.interactive {
			// Continue with dry run after API key setup
			return a.executeDryRun, nil
		}

		// In interactive mode, transition to auth selection state
		a.sendSelect(out, "Choose authentication method:", []string{
			"Interactive login (recommended)",
			"Enter API key directly",
		})
		// Transition to waiting for auth selection (but for dry run)
		return a.waitForAuthSelectionDryRun, nil
	}

	// Already authenticated, proceed with dry run
	return a.executeDryRun(ctx, input, out)
}

func (a *Agent) waitForAuthSelectionDryRun(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	input = strings.TrimSpace(input)

	switch input {
	case "0": // First option - Interactive login
		return a.performOAuthLoginDryRun(ctx, input, out)
	case "1": // Second option - API key
		return a.promptForAPIKeyDryRun(ctx, input, out)
	default:
		// Invalid selection, ask again
		a.sendSelect(out, "Choose authentication method:", []string{
			"Interactive login (recommended)",
			"Enter API key directly",
		})
		return a.waitForAuthSelectionDryRun, nil
	}
}

func (a *Agent) promptForAPIKeyDryRun(_ context.Context, _ string, out io.Writer) (stateFn, error) {
	// Send API key prompt
	a.sendAPIKeyPrompt(out, "🔑 Enter your Render API key (get it from https://dashboard.render.com/account/settings):")
	return a.waitForAPIKeyDryRun, nil
}

func (a *Agent) waitForAPIKeyDryRun(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	apiKey := strings.TrimSpace(input)

	// Validate API key format
	if len(apiKey) == 0 {
		fmt.Fprint(out, "❌ API key cannot be empty\n")
		return a.promptForAPIKeyDryRun(ctx, "", out)
	}

	if len(apiKey) < 20 {
		fmt.Fprint(out, "❌ API key seems too short (should be at least 20 characters)\n")
		return a.promptForAPIKeyDryRun(ctx, "", out)
	}

	if !strings.HasPrefix(apiKey, "rnd_") {
		fmt.Fprint(out, "❌ Render API keys typically start with 'rnd_'\n")
		return a.promptForAPIKeyDryRun(ctx, "", out)
	}

	// Set the API key in the environment
	os.Setenv("RENDER_API_KEY", apiKey)

	// Validate the API key by making a test call
	fmt.Fprint(out, "🔍 Validating API key...\n")
	valid, err := a.auth.ValidateAPIKey(ctx, apiKey)
	if err != nil {
		fmt.Fprintf(out, "❌ Failed to validate API key: %v\n", err)
		os.Unsetenv("RENDER_API_KEY")
		return a.promptForAPIKeyDryRun(ctx, "", out)
	}

	if !valid {
		fmt.Fprint(out, "❌ Invalid API key - please check your key and try again\n")
		os.Unsetenv("RENDER_API_KEY")
		return a.promptForAPIKeyDryRun(ctx, "", out)
	}

	fmt.Fprint(out, "✅ API key validated successfully!\n")
	fmt.Fprint(out, "💡 API key will only be available for this session.\n")
	fmt.Fprint(out, "   To persist it manually, run: export RENDER_API_KEY=your_key_here\n")

	// Continue with dry run
	return a.executeDryRun(ctx, input, out)
}

func (a *Agent) performOAuthLoginDryRun(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprint(out, "🚀 Starting authentication flow...\n")

	// Perform OAuth login using the auth package
	if err := a.auth.PerformOAuthLogin(ctx); err != nil {
		fmt.Fprintf(out, "❌ Authentication failed: %v\n", err)
		fmt.Fprint(out, "🔧 You can try option 2 (Manual API key setup) instead\n")
		return a.waitForAuthSelectionDryRun, nil
	}

	fmt.Fprint(out, "✅ Authentication successful!\n")

	// Continue with dry run
	return a.executeDryRun(ctx, input, out)
}

func (a *Agent) executeDryRun(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	wf, err := Workflows{}.DryRunDeploy(ctx, a.wfClient, *a.DeployPlan)
	if err != nil {
		log.Printf("Dry-run workflow execution result: %v\n", err)
		fmt.Fprint(out, "Sorry, couldn't create a dry-run deployment plan \n")
		return a.plan, nil
	}

	// get the dry-run result with a longer timeout to accommodate LLM operations
	result, err := client.GetWorkflowResult[DryRunResult](ctx, a.wfClient, wf, 5*time.Minute)
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

func (a *Agent) authenticateCLI(ctx context.Context) bool {
	err := a.internalAuth.LoginWithBrowser(ctx)
	if err != nil {
		slog.Error("Failed to authenticate CLI", "error", err)
		return false
	}
	return true
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
