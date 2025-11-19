package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/auth"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/heroku"
	"github.com/meroxa/prod/cli/internal/deployment/netlify"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	prod_error "github.com/meroxa/prod/cli/internal/error"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/settings"
)

type TUIWriter interface {
	io.Writer
	SendConfirmation(message string, callback func(bool))
	SendAPIKeyPrompt(message string)
	SendSelect(message string, options []string)
	SendTextPrompt(message string)
	SendTextPromptWithDefault(message string, defaultValue string)
	SendPlan(plan DeployPlan)
	SendError(summary string, remediations []Remediation)
	SendWarning(summary string, remediations []Remediation)
	SendSuccess(platform string, appName string, url string)
	SendDeploymentHistory(deployments []backend.DeploymentHistoryItem)
	StopSpinner()
	ClearScreen()
	Quit()
	Search()
}

type EnvVarWithStatus struct {
	deployment.EnvVar
	Status string // "pending", "collected", "db_related"
}

type Agent struct {
	sm                   deploySM
	wfClient             *client.Client
	interactive          bool
	DeployPlan           *DeployPlan
	UIOutput             io.Writer
	auth                 auth.AuthProvider
	envVars              []EnvVarWithStatus
	internalAuth         *auth.SupabaseAuth
	errorTrackingEnabled bool
	inConsentFlow        bool
	originalInput        string
	nextStateAfterAuth   stateFn // State to transition to after successful PaaS authentication
	awsRegion            string  // Selected AWS region for deployment
}

type agentContextKey string

const agentAuthSession agentContextKey = "AuthSession"

func NewAgent(wfClient *client.Client, internalAuth *auth.SupabaseAuth, errorTrackingEnabled bool) *Agent {
	a := &Agent{
		wfClient:             wfClient,
		interactive:          true, // Default to interactive
		envVars:              make([]EnvVarWithStatus, 0),
		internalAuth:         internalAuth,
		errorTrackingEnabled: errorTrackingEnabled,
	}
	sm := deploySM{currentState: a.checkPrerequisites}
	a.sm = sm
	return a
}

type DeployPlan struct {
	Action              Action
	Platform            Platform
	Source              string
	Spec                analyzer.ProjectSpec
	Summary             string
	CollectedEnvVars    []deployment.EnvVar
	Pricing             deployment.CostEstimate
	ExistingProjectInfo ExistingProjectInfo
}

type deployResult struct {
	Url   string
	Error deployError
}

type deployError struct {
	Summary      string
	Remediations []Remediation
	IsWarning    bool
}

type Remediation struct {
	Description string
	CliCommand  string
}

//go:generate stringer -type=Platform,Action -output=types_string.go
type Platform int

const (
	Render Platform = iota
	FlyIO
	Netlify
	Vercel
	Heroku
	AWS
	UnknownPlatform
)

type Action int

const (
	Deploy Action = iota
	Rollback
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
		slog.Info("Error in state", "stateType", fmt.Sprintf("%T", sm.currentState), "error", err)
		return err
	}

	sm.currentState = nextState
	return nil
}

func (a *Agent) SetInteractive(interactive bool) {
	a.interactive = interactive
}

func (a *Agent) IsErrorTrackingEnabled() bool {
	return a.errorTrackingEnabled
}

// Helper methods for TUI operations - direct TUI calls
func (a *Agent) sendPlan(out io.Writer, plan DeployPlan) {
	tuiWriter := out.(TUIWriter)
	tuiWriter.SendPlan(plan)
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
	slog.Info("Processing input", "input", input)
	output := out
	if a.UIOutput != nil {
		output = a.UIOutput
	}

	// Handle slash commands - they bypass the state machine
	if strings.HasPrefix(strings.TrimSpace(input), "/") {
		nextState, err := a.handleSlashCommand(ctx, input, output)
		if err != nil {
			slog.Error("Error handling slash command", "error", err)
			return
		}
		// Update the state machine's current state if the command returns one
		if nextState != nil {
			a.sm.currentState = nextState
		}
		return
	}

	// Enrich context with session if authenticated
	contextWithSession := ctx
	if a.internalAuth.IsAuthenticated() {
		session, err := a.internalAuth.GetSession()
		if err != nil {
			slog.Error("Failed to get session", "error", err)
		} else {
			contextWithSession = WithCtxSession(ctx, session)
		}
	}

	// Delegate everything else to the state machine
	a.sm.next(contextWithSession, input, output)
}

func (a *Agent) checkPrerequisites(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	slog.Info("checkPrerequisites called", "isAuthenticated", a.internalAuth.IsAuthenticated())

	// If we're in consent flow, continue with that
	if a.inConsentFlow {
		return nil, nil // The consent states will handle the flow
	}

	// Check for error tracking consent first
	if !a.errorTrackingEnabled {
		hasConsentValue, err := settings.HasConsent()
		if err != nil {
			slog.Error("Failed to check consent", "error", err)
		} else if !hasConsentValue {
			// Check if settings file exists - if not, this is first run
			filePath, err := settings.GetSettingsPath()
			if err == nil {
				if _, err := os.Stat(filePath); os.IsNotExist(err) {
					// First run - need to prompt for consent using state machine
					a.inConsentFlow = true
					a.originalInput = input // Store original input to use after consent
					return a.promptForConsent(ctx, input, out)
				}
			}
		} else {
			a.errorTrackingEnabled = true
		}
	}

	// Always check authentication when checkPrerequisites is called
	// This ensures auth is validated on every new user input
	if !a.ensureAuthenticated(ctx, out) {
		return a.checkPrerequisites, nil
	}

	// Get session and enrich context after successful authentication
	session, err := a.internalAuth.GetSession()
	if err != nil {
		slog.Error("Failed to get session", "error", err)
	}
	ctxWithSession := WithCtxSession(ctx, session)

	// Both consent and auth are complete, proceed to plan
	return a.plan(ctxWithSession, input, out)
}

func (a *Agent) promptForConsent(_ context.Context, _ string, out io.Writer) (stateFn, error) {
	a.inConsentFlow = true
	fmt.Fprint(out, `
📊 We'd like to collect anonymous diagnostic data to help improve Prod CLI.
   This helps us identify and fix issues faster. No personal information 
   or code content is collected.

`)
	a.sendConfirmation(out, "Would you like to enable error tracking?")
	return a.waitForConsentResponse, nil
}

func (a *Agent) waitForConsentResponse(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	return a.processConsentResponse(ctx, input, out)
}

func (a *Agent) processConsentResponse(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	input = strings.TrimSpace(strings.ToLower(input))

	var consentGiven bool
	switch input {
	case "y", "yes":
		consentGiven = true
		fmt.Fprint(out, "✅ Diagnostics tracking enabled. Thank you!\n")
	case "n", "no":
		consentGiven = false
		fmt.Fprint(out, "✅ Diagnostics tracking disabled.\n")
	default:
		// Invalid response - ask again
		a.sendConfirmation(out, "Would you like to enable error tracking?")
		return a.waitForConsentResponse, nil
	}

	// Save the consent choice
	err := settings.SaveConsent(consentGiven)
	if err != nil {
		slog.Error("Failed to save consent", "error", err)
	} else {
		a.errorTrackingEnabled = consentGiven
	}

	// Clear consent flow flag
	a.inConsentFlow = false

	// Continue with the original input through checkPrerequisites
	// This will handle authentication and then proceed to plan
	if a.originalInput != "" {
		originalInput := a.originalInput
		a.originalInput = "" // Clear it
		return a.checkPrerequisites(ctx, originalInput, out)
	}

	// No original input, go back to checkPrerequisites which will proceed to plan
	return a.checkPrerequisites, nil
}

func (a *Agent) handleSlashCommand(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return a.checkPrerequisites, nil
	}

	commandName := parts[0]

	// Find and execute the matching command
	for _, cmd := range a.GetAvailableSlashCommands() {
		if cmd.Name == commandName {
			return cmd.Handler(ctx, out)
		}
	}

	// Command not found
	fmt.Fprintf(out, "Unknown command: %s\n", commandName)
	return a.checkPrerequisites, nil
}

func (a *Agent) plan(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	wf, err := Workflows{}.PlanDeploy(ctx, a.wfClient, input)
	if err != nil {
		slog.Info("Workflow execution result", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":  "plan_deploy",
			"component": "agent",
			"operation": "workflow_execution",
		})
	}

	plan, err := client.GetWorkflowResult[DeployPlan](ctx, a.wfClient, wf, 5*time.Minute)
	if err != nil {
		fmt.Fprintf(out, "Error getting workflow result: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":  "plan_deploy",
			"component": "agent",
			"operation": "get_workflow_result",
		})
	}

	a.sendPlan(out, plan)

	if !shouldProceed(plan) {
		fmt.Fprintf(out, "Cannot proceed with deployment plan\n")
		return a.checkPrerequisites, nil
	}
	a.DeployPlan = &plan

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
		// Check if this is a rollback or deploy
		if a.DeployPlan.Action == Rollback {
			// For rollback, check if we need platform selection
			if len(a.DeployPlan.ExistingProjectInfo.DetectedPlatforms) > 1 {
				fmt.Fprintf(out, "Proceeding with rollback...\n")
				return a.selectPlatform(ctx, input, out)
			}
			// Single platform or platform from prompt - proceed to auth check
			fmt.Fprintf(out, "Proceeding with rollback...\n")
			a.nextStateAfterAuth = a.executeRollback
			return a.checkAuthentication(ctx, input, out)
		}

		// Deploy flow
		fmt.Fprintf(out, "Proceeding with deployment...\n")
		a.nextStateAfterAuth = a.detectExisting
		return a.checkAuthentication(ctx, input, out)
	}

	if input == "n" || input == "no" {
		if a.DeployPlan.Action == Rollback {
			fmt.Fprintf(out, "Rollback cancelled\n")
		} else {
			fmt.Fprintf(out, "Deployment cancelled\n")
		}
		return a.checkPrerequisites, nil
	}

	// Invalid response - ask again
	if a.DeployPlan.Action == Rollback {
		a.sendConfirmation(out, "Do you want to proceed with the rollback?")
	} else {
		a.sendConfirmation(out, "Do you want to proceed with the deployment?")
	}
	return a.waitForConfirmation, nil
}

func (a *Agent) confirm(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	return a.deploy, nil
}

func (a *Agent) detectExisting(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	// Clear nextStateAfterAuth since we're now in detection
	a.nextStateAfterAuth = nil

	fmt.Fprintf(out, "🔍 Checking for existing resources...\n")

	wf, err := Workflows{}.DetectExisting(ctx, a.wfClient, *a.DeployPlan)
	if err != nil {
		fmt.Fprintf(out, "❌ Error detecting existing resources: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "detect_existing",
			"component":    "agent",
			"operation":    "workflow_execution",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
		})
		return a.checkPrerequisites, nil
	}

	result, err := client.GetWorkflowResult[ExistingProjectInfo](ctx, a.wfClient, wf, 2*time.Minute)
	if err != nil {
		fmt.Fprintf(out, "❌ Error getting detection results: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "detect_existing",
			"component":    "agent",
			"operation":    "get_workflow_result",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
		})
		return a.checkPrerequisites, nil
	}

	a.DeployPlan.ExistingProjectInfo = result

	var summaryText string
	if result.Exists {
		summaryText = "🔍 Existing Resources Found:\n\n"
		summaryText += fmt.Sprintf("• Application: %s (will be updated)\n", result.Name)

		if len(result.ExistingDatabases) > 0 {
			summaryText += "\n• Databases (will be reused):\n"
			for _, db := range result.ExistingDatabases {
				summaryText += fmt.Sprintf("  - %s\n", db)
			}
		}

		needsToCreate := []string{}
		for _, service := range a.DeployPlan.Spec.ServiceRequirements {
			// Only include actual infrastructure resources (database, cache)
			if service.Type != "database" && service.Type != "cache" {
				continue
			}
			if !slices.Contains(result.ExistingDatabases, service.Provider) {
				needsToCreate = append(needsToCreate, service.Provider)
			}
		}

		if len(needsToCreate) > 0 {
			summaryText += "\n📦 New Resources to Create:\n"
			for _, service := range needsToCreate {
				summaryText += fmt.Sprintf("• %s database\n", service)
			}
		}
	} else {
		summaryText = "📦 New Deployment:\n\n"
		summaryText += fmt.Sprintf("• Application: %s (new)\n", a.DeployPlan.Spec.Name)

		// Count only actual infrastructure resources (database, cache)
		databases := []string{}
		for _, service := range a.DeployPlan.Spec.ServiceRequirements {
			// Only include actual infrastructure resources
			if service.Type != "database" && service.Type != "cache" {
				continue
			}
			databases = append(databases, service.Provider)
		}

		if len(databases) > 0 {
			summaryText += "\n• Databases (new):\n"
			for _, db := range databases {
				summaryText += fmt.Sprintf("  - %s\n", db)
			}
		}
	}

	if tuiWriter, ok := out.(output.InfoBoxWriter); ok {
		tuiWriter.SendInfoBox("Deployment Plan", summaryText, "📋")
	} else {
		fmt.Fprintf(out, "\n%s\n", summaryText)
	}

	return a.categorizeEnvironmentVariables(ctx, input, out)
}

func (a *Agent) categorizeEnvironmentVariables(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprintf(out, "🔍 Categorizing environment variables...\n")

	wf, err := Workflows{}.CategorizeEnvVars(ctx, a.wfClient, *a.DeployPlan)
	if err != nil {
		fmt.Fprintf(out, "❌ Error categorizing environment variables: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "categorize_env_vars",
			"component":    "agent",
			"operation":    "workflow_execution",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
			"language":     a.DeployPlan.Spec.Language,
		})
		return a.deploy(ctx, input, out)
	}

	envVars, err := client.GetWorkflowResult[[]deployment.EnvVar](ctx, a.wfClient, wf, 5*time.Minute)
	if err != nil {
		fmt.Fprintf(out, "❌ Error getting categorization result: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "categorize_env_vars",
			"component":    "agent",
			"operation":    "get_workflow_result",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
			"language":     a.DeployPlan.Spec.Language,
		})
		return a.deploy(ctx, input, out)
	}

	fmt.Fprintf(out, "✅ Environment variables categorized\n")

	// always initialize envVars slice to reset between deploys
	a.envVars = make([]EnvVarWithStatus, 0)

	// Process all environment variables and set their status
	var pendingCount int
	var sensitivePending []string
	var sensitiveAutoPopulated []string
	var nonSensitiveAutoPopulated []string

	for _, envVar := range envVars {
		if envVar.IsNotDBRelated() {
			// This non-DB var needs user input
			a.envVars = append(a.envVars, EnvVarWithStatus{
				EnvVar: envVar,
				Status: "pending",
			})
			pendingCount++
			if envVar.Sensitive {
				sensitivePending = append(sensitivePending, envVar.Name)
			}
		} else {
			// DB-related vars - deployment system will handle values
			a.envVars = append(a.envVars, EnvVarWithStatus{
				EnvVar: envVar,
				Status: "db_related",
			})
			if envVar.Sensitive {
				sensitiveAutoPopulated = append(sensitiveAutoPopulated, envVar.Name)
			} else {
				nonSensitiveAutoPopulated = append(nonSensitiveAutoPopulated, envVar.Name)
			}
		}
	}

	// Display all auto-populated variables (both sensitive and non-sensitive)
	totalAutoPopulated := len(sensitiveAutoPopulated) + len(nonSensitiveAutoPopulated)
	if totalAutoPopulated > 0 {
		fmt.Fprintf(out, "🔄 The following variables will be auto-populated:\n")
		// Show non-sensitive first
		for _, name := range nonSensitiveAutoPopulated {
			fmt.Fprintf(out, "  • %s\n", name)
		}
		// Then sensitive ones with lock icon
		for _, name := range sensitiveAutoPopulated {
			fmt.Fprintf(out, "  • %s 🔒 (sensitive)\n", name)
		}
		fmt.Fprint(out, "\n")
	}

	// Display variables that need input (including sensitive ones)
	if pendingCount > 0 {
		fmt.Fprintf(out, "Found %d environment variable(s) that need values:\n", pendingCount)
		for _, envVar := range a.envVars {
			if envVar.Status == "pending" {
				if envVar.Sensitive {
					fmt.Fprintf(out, "  • %s 🔒 (sensitive)\n", envVar.Name)
				} else {
					fmt.Fprintf(out, "  • %s\n", envVar.Name)
				}
			}
		}
		fmt.Fprint(out, "\n")
		if len(sensitivePending) > 0 {
			fmt.Fprintf(out, "🔒 Sensitive variables are marked with 🔒. We'll display the values you enter in plaintext, but they are handled securely when we deploy!\n")
		}
		return a.promptForEnvVarValue(ctx, input, out)
	}

	// All env vars are database-related or already have values, proceed with PrepareJS
	fmt.Fprintf(out, "✅ All environment variables are ready. Proceeding to JavaScript preparation...\n")
	return a.prepareJS(ctx, input, out)
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
		// No more pending env vars, proceed with PrepareJS
		fmt.Fprintf(out, "All environment variable values collected. Proceeding to JavaScript preparation...\n")
		return a.prepareJS(ctx, input, out)
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

// unescapeJSONUnicode converts JSON unicode escapes like \u0026 back to their original characters
func unescapeJSONUnicode(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\\u0026", "&"), "\\u0026\\u0026", "&&")
}

func (a *Agent) prepareJS(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	if a.DeployPlan.Spec.Language == "node" {
		fmt.Fprintf(out, "🔧 Preparing JavaScript environment...\n")
		wf, err := Workflows{}.SetupJavaScriptProject(ctx, a.wfClient, *a.DeployPlan)
		if err != nil {
			slog.Error("Workflow execution result", "error", err)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     "setup_javascript_project",
				"component":    "agent",
				"platform":     a.DeployPlan.Platform,
				"project_type": "javascript",
			})
			fmt.Fprint(out, "Sorry, couldn't create a deployment plan \n")
			return a.checkPrerequisites, nil
		}

		result, err := client.GetWorkflowResult[SetupJavaScriptProjectResult](ctx, a.wfClient, wf, 2*time.Minute)
		if err != nil {
			fmt.Fprint(out, "Once you are ready to retry, just let me know!\n")
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     "setup_javascript_project",
				"component":    "agent",
				"operation":    "get_workflow_result",
				"platform":     a.DeployPlan.Platform,
				"project_name": a.DeployPlan.Spec.Name,
				"language":     a.DeployPlan.Spec.Language,
			})
			return a.confirmWithPrompt(ctx, "", out)
		}
		if result.Error.Summary != "" {
			if tuiWriter, ok := out.(TUIWriter); ok {
				if result.Error.IsWarning {
					tuiWriter.SendWarning(result.Error.Summary, result.Error.Remediations)
				} else {
					tuiWriter.SendError(result.Error.Summary, result.Error.Remediations)
				}
			} else {
				if result.Error.IsWarning {
					fmt.Fprintf(out, "⚠️  %s\n", result.Error.Summary)
				} else {
					fmt.Fprintf(out, "❌ %s\n", result.Error.Summary)
				}
				if len(result.Error.Remediations) > 0 {
					fmt.Fprint(out, "Here are some suggestions to fix the issues:\n")
					for _, r := range result.Error.Remediations {
						fmt.Fprintf(out, " • %s\n", r.Description)
						if r.CliCommand != "" {
							fmt.Fprintf(out, "   Run: %s\n", r.CliCommand)
						}
					}
				}
				fmt.Fprint(out, "Once you are ready to retry, just let me know!\n")
			}
			return a.confirmWithPrompt(ctx, "", out)
		}
		// Display config diff if available
		if len(result.ConfigDiff) > 0 {
			fmt.Fprintf(out, "\n⚙️ %s configuration changes:\n", result.ConfigPath)
			fmt.Fprint(out, "────────────────────────────────────────\n")

			for _, line := range result.ConfigDiff {
				content := unescapeJSONUnicode(line.Content)
				switch line.Type {
				case "header":
					fmt.Fprintf(out, "\033[36m%s\033[0m\n", content)
				case "added":
					fmt.Fprintf(out, "\033[32m%s\033[0m\n", content)
				case "removed":
					fmt.Fprintf(out, "\033[31m%s\033[0m\n", content)
				case "fileheader":
					fmt.Fprintf(out, "\033[1m%s\033[0m\n", content)
				default:
					fmt.Fprintf(out, "%s\n", content)
				}
			}
			fmt.Fprint(out, "────────────────────────────────────────\n")
		}

		// Display package.json diff if available
		if len(result.PackageJsonDiff) > 0 {
			fmt.Fprint(out, "\n📦 Package.json changes:\n")
			fmt.Fprint(out, "────────────────────────────────────────\n")

			for _, line := range result.PackageJsonDiff {
				content := unescapeJSONUnicode(line.Content)
				switch line.Type {
				case "header":
					fmt.Fprintf(out, "\033[36m%s\033[0m\n", content)
				case "added":
					fmt.Fprintf(out, "\033[32m%s\033[0m\n", content)
				case "removed":
					fmt.Fprintf(out, "\033[31m%s\033[0m\n", content)
				case "fileheader":
					fmt.Fprintf(out, "\033[1m%s\033[0m\n", content)
				default:
					fmt.Fprintf(out, "%s\n", content)
				}
			}
			fmt.Fprint(out, "────────────────────────────────────────\n")
		}
		a.DeployPlan = &result.UpdatedPlan
		fmt.Fprint(out, "✅ JavaScript environment prepared successfully!\n")

	}

	// After PrepareJS completion, proceed with deployment
	return a.deploy(ctx, input, out)
}

func (a *Agent) deploy(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	// Check authentication before deployment
	return a.checkAuthentication(ctx, input, out)
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
	// make sure if we have collected any other env vars along the way they are captured
	DeployPlanWithEnvVars.CollectedEnvVars = append(DeployPlanWithEnvVars.CollectedEnvVars, collectedEnvVars...)

	wf, err := Workflows{}.Deploy(ctx, a.wfClient, DeployPlanWithEnvVars)
	if err != nil {
		slog.Info("Workflow execution result", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "deploy",
			"component":    "agent",
			"operation":    "workflow_execution",
			"platform":     DeployPlanWithEnvVars.Platform,
			"project_name": DeployPlanWithEnvVars.Spec.Name,
			"language":     DeployPlanWithEnvVars.Spec.Language,
		})
		fmt.Fprint(out, "Sorry, couldn't create a deployment plan \n")
		return a.checkPrerequisites, nil
	}

	// give a generous timeout for the deployment to complete
	result, err := client.GetWorkflowResult[deployResult](ctx, a.wfClient, wf, 20*time.Minute)
	// manually stop the spinner in case anything is dangling from the deploy workflow
	a.stopSpinner(out)

	if err != nil {
		slog.Info("Deployment workflow execution result", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "deploy",
			"component":    "agent",
			"operation":    "get_workflow_result",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
			"language":     a.DeployPlan.Spec.Language,
		})
		a.wfClient.CancelWorkflowInstance(ctx, wf)
		fmt.Fprint(out, "Sorry, we had trouble deploying your project \n")
		return a.checkPrerequisites, nil
	}

	if result.Error.Summary != "" {
		if tuiWriter, ok := out.(TUIWriter); ok {
			if result.Error.IsWarning {
				tuiWriter.SendWarning(result.Error.Summary, result.Error.Remediations)
			} else {
				tuiWriter.SendError(result.Error.Summary, result.Error.Remediations)
			}
		} else {
			if result.Error.IsWarning {
				fmt.Fprint(out, "⚠️  Deployment warning\n")
			} else {
				fmt.Fprint(out, "Sorry, we had trouble deploying your project \n")
			}
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
			}
		}

		if len(result.Error.Remediations) > 0 {
			return a.confirmWithPrompt(ctx, "", out)
		}
		if !a.interactive {
			return nil, nil
		}
		return a.checkPrerequisites, nil
	}

	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendSuccess(a.DeployPlan.Platform.String(), a.DeployPlan.Spec.Name, result.Url)
		if result.Url != "" {
			openInBrowser(result.Url)
		}
	} else {
		io.WriteString(out, "Deployed!...🚀\n")
		if result.Url != "" {
			fmt.Fprintf(out, "You can access your deployment at: %s\n", result.Url)
			openInBrowser(result.Url)
		}
	}

	// In non-interactive mode, end the state machine
	if !a.interactive {
		return nil, nil
	}
	// In interactive mode, return to input processing state for more commands
	return a.checkPrerequisites, nil
}

func (a *Agent) selectPlatform(_ context.Context, _ string, out io.Writer) (stateFn, error) {
	// Build platform options from detected platforms
	platformOptions := make([]string, len(a.DeployPlan.ExistingProjectInfo.DetectedPlatforms))
	for i, p := range a.DeployPlan.ExistingProjectInfo.DetectedPlatforms {
		platformOptions[i] = p.String()
	}

	a.sendSelect(out, "Multiple deployments found. Which platform would you like to rollback?", platformOptions)
	return a.waitForPlatformSelection, nil
}

func (a *Agent) waitForPlatformSelection(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	input = strings.TrimSpace(input)

	// Parse the selection index
	selectionIndex := -1
	_, err := fmt.Sscanf(input, "%d", &selectionIndex)
	if err != nil || selectionIndex < 0 || selectionIndex >= len(a.DeployPlan.ExistingProjectInfo.DetectedPlatforms) {
		// Invalid selection
		platformOptions := make([]string, len(a.DeployPlan.ExistingProjectInfo.DetectedPlatforms))
		for i, p := range a.DeployPlan.ExistingProjectInfo.DetectedPlatforms {
			platformOptions[i] = p.String()
		}
		a.sendSelect(out, "Invalid selection. Which platform would you like to rollback?", platformOptions)
		return a.waitForPlatformSelection, nil
	}

	// Set the selected platform
	selectedPlatform := a.DeployPlan.ExistingProjectInfo.DetectedPlatforms[selectionIndex]
	a.DeployPlan.Platform = selectedPlatform

	slog.Info("User selected platform for rollback", "platform", selectedPlatform)
	fmt.Fprintf(out, "Selected %s for rollback\n", selectedPlatform.String())

	// Now proceed to auth check
	a.nextStateAfterAuth = a.executeRollback
	return a.checkAuthentication(ctx, input, out)
}

func (a *Agent) executeRollback(ctx context.Context, _ string, out io.Writer) (stateFn, error) {
	a.nextStateAfterAuth = nil

	wf, err := Workflows{}.Rollback(ctx, a.wfClient, *a.DeployPlan)
	if err != nil {
		slog.Error("Rollback workflow execution failed", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "rollback",
			"component":    "agent",
			"operation":    "workflow_execution",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
		})
		fmt.Fprint(out, "Sorry, couldn't execute the rollback\n")
		return a.checkPrerequisites, nil
	}

	result, err := client.GetWorkflowResult[deployResult](ctx, a.wfClient, wf, 10*time.Minute)
	a.stopSpinner(out)

	if err != nil {
		slog.Error("Rollback workflow result error", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     "rollback",
			"component":    "agent",
			"operation":    "get_workflow_result",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
		})
		a.wfClient.CancelWorkflowInstance(ctx, wf)
		fmt.Fprint(out, "Sorry, we had trouble rolling back your deployment\n")
		return a.checkPrerequisites, nil
	}

	if result.Error.Summary != "" {
		if tuiWriter, ok := out.(TUIWriter); ok {
			tuiWriter.SendError(result.Error.Summary, result.Error.Remediations)
		} else {
			fmt.Fprint(out, "Sorry, we had trouble rolling back your deployment\n")
			fmt.Fprintf(out, "%s\n", result.Error.Summary)
		}
		if !a.interactive {
			return nil, nil
		}
		return a.checkPrerequisites, nil
	}

	// Success
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendSuccess(a.DeployPlan.Platform.String(), a.DeployPlan.Spec.Name, result.Url)
		if result.Url != "" {
			openInBrowser(result.Url)
		}
	} else {
		fmt.Fprint(out, "✅ Rollback completed successfully!\n")
		if result.Url != "" {
			fmt.Fprintf(out, "Your application is now at: %s\n", result.Url)
			openInBrowser(result.Url)
		}
	}

	if !a.interactive {
		return nil, nil
	}
	return a.checkPrerequisites, nil
}

func shouldProceed(plan DeployPlan) bool {
	if plan.Action == UnknownAction {
		slog.Info("Validation failed", "reason", "unknown action", "action", plan.Action)
		return false
	}

	if plan.Platform == UnknownPlatform {
		if plan.Action == Rollback && len(plan.ExistingProjectInfo.DetectedPlatforms) > 0 {
			slog.Info("Validation passed for rollback with multiple platforms", "platforms", plan.ExistingProjectInfo.DetectedPlatforms)
		} else {
			slog.Info("Validation failed", "reason", "unknown platform", "platform", plan.Platform)
			return false
		}
	}

	if plan.Spec.Name == "" || plan.Spec.Language == "" {
		slog.Info("Validation failed", "reason", "missing spec fields", "name", plan.Spec.Name, "language", plan.Spec.Language)
		return false
	}

	slog.Info("Validation passed", "status", "successful")
	return true
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
		return a.checkPrerequisites, err
	}

	if !authenticated {
		fmt.Fprintf(out, "🔐 Authentication required for %s deployment\n\n", a.DeployPlan.Platform)

		// Store the auth provider for use in authentication states
		a.auth = authProvider

		// In non-interactive mode, if we are not authenticated exit state machine
		if !a.interactive {
			return nil, nil
		}

		// AWS has a custom CloudFormation-based auth flow
		// First prompt for region selection, then proceed to auth setup
		if a.DeployPlan.Platform == AWS {
			return a.promptForAWSRegion(ctx, input, out)
		}

		// In interactive mode, transition to auth selection state for other platforms
		a.sendSelect(out, "Choose authentication method:", []string{
			"Interactive login (recommended)",
			"Enter API key directly",
		})
		// Transition to waiting for auth selection
		return a.waitForAuthSelection, nil
	}

	// Already authenticated, proceed to next state (detection or deployment)
	if a.nextStateAfterAuth != nil {
		nextState := a.nextStateAfterAuth
		a.nextStateAfterAuth = nil // Clear it after use
		return nextState(ctx, input, out)
	}
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
		netlifyClient := netlify.NewCLINetlifyClient()
		netlifyAuth := auth.NewNetlifyAuth(netlifyClient, out)
		return netlifyAuth, nil
	case Vercel:
		vercelAuth := auth.NewVercelAuth(out)
		return vercelAuth, nil
	case Heroku:
		herokuClient := heroku.NewHerokuClient("", output.NewNoOpWriter())
		herokuAuth := auth.NewHerokuAuth(herokuClient, out)
		return herokuAuth, nil
	case AWS:
		awsAuth := auth.NewAWSAuth(out)
		awsAuth.SetSessionExtractor(CtxSession)
		return awsAuth, nil
	default:
		return nil, errors.Errorf("unsupported platform: %s", a.DeployPlan.Platform)
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
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"component": "agent",
			"operation": "api_key_validation",
			"auth_type": "api_key",
			"platform":  a.DeployPlan.Platform.String(),
			"flow":      "deployment",
		})
		return a.promptForAPIKey(ctx, "", out)
	}

	if !valid {
		fmt.Fprint(out, "❌ Invalid API key - please check your key and try again\n")
		return a.promptForAPIKey(ctx, "", out)
	}

	fmt.Fprint(out, "✅ API key validated successfully!\n")
	fmt.Fprint(out, "💡 API key will only be available for this session.\n")

	// Continue to next state (detection or deployment)
	if a.nextStateAfterAuth != nil {
		return a.nextStateAfterAuth(ctx, input, out)
	}
	return a.executeDeployment(ctx, input, out)
}

func (a *Agent) promptForAWSRegion(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprint(out, "🌍 Enter your preferred AWS region for deployment\n")
	fmt.Fprint(out, "   Common regions: us-east-1, us-west-2, eu-west-1, ap-southeast-1\n\n")
	a.sendTextPromptWithDefault(out, "AWS Region:", "us-east-1")
	return a.waitForAWSRegionInput, nil
}

func (a *Agent) waitForAWSRegionInput(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	region := strings.TrimSpace(input)

	// If empty, use default
	if region == "" {
		region = "us-east-1"
	}

	// Basic validation for AWS region format (e.g., us-east-1, eu-west-2)
	// Simple format check: should contain at least one dash
	if !strings.Contains(region, "-") {
		fmt.Fprintf(out, "Invalid region format: %s\n", region)
		fmt.Fprint(out, "AWS regions should be in format like 'us-east-1' or 'eu-west-2'\n\n")
		a.sendTextPromptWithDefault(out, "AWS Region:", "us-east-1")
		return a.waitForAWSRegionInput, nil
	}

	a.awsRegion = region
	fmt.Fprintf(out, "Using region: %s\n\n", region)
	return a.performAWSAuth(ctx, input, out)
}

func (a *Agent) performAWSAuth(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprintf(out, "🚀 Setting up AWS authentication for region: %s\n", a.awsRegion)
	fmt.Fprint(out, "We'll guide you through creating an IAM role in your AWS account.\n\n")

	// Set the region on the AWS auth provider
	awsAuth, ok := a.auth.(*auth.AWSAuth)
	if !ok {
		return nil, errors.Errorf("auth provider is not AWSAuth")
	}
	awsAuth.SetRegion(a.awsRegion)

	// Initialize AWS auth setup (get external ID, show CloudFormation URL)
	if err := awsAuth.InitializeSetup(ctx); err != nil {
		fmt.Fprintf(out, "❌ Failed to initialize AWS setup: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"component": "agent",
			"operation": "aws_auth_init",
			"auth_type": "cloudformation",
			"platform":  "aws",
			"region":    a.awsRegion,
			"flow":      "deployment",
		})
		return a.checkPrerequisites, err
	}

	// Prompt user for Role ARN
	a.sendTextPrompt(out, "Paste the Role ARN from CloudFormation stack outputs:")
	return a.waitForAWSRoleArn, nil
}

func (a *Agent) waitForAWSRoleArn(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	roleArn := strings.TrimSpace(input)

	// Basic validation
	if roleArn == "" {
		fmt.Fprint(out, "❌ Role ARN cannot be empty\n")
		a.sendTextPrompt(out, "Paste the Role ARN from CloudFormation stack outputs:")
		return a.waitForAWSRoleArn, nil
	}

	// Validate ARN format
	if !strings.HasPrefix(roleArn, "arn:aws:iam::") || !strings.Contains(roleArn, ":role/") {
		fmt.Fprintf(out, "❌ Invalid Role ARN format: %s\n", roleArn)
		fmt.Fprint(out, "Expected format: arn:aws:iam::123456789012:role/RoleName\n")
		a.sendTextPrompt(out, "Paste the Role ARN from CloudFormation stack outputs:")
		return a.waitForAWSRoleArn, nil
	}

	// Complete the AWS auth setup
	awsAuth, ok := a.auth.(*auth.AWSAuth)
	if !ok {
		return nil, errors.Errorf("auth provider is not AWSAuth")
	}

	fmt.Fprint(out, "💾 Saving AWS credentials...\n")
	if err := awsAuth.CompleteSetup(ctx, roleArn); err != nil {
		fmt.Fprintf(out, "❌ Failed to save AWS credentials: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"component": "agent",
			"operation": "aws_auth_complete",
			"auth_type": "cloudformation",
			"platform":  "aws",
			"region":    a.awsRegion,
			"flow":      "deployment",
		})
		a.sendTextPrompt(out, "Paste the Role ARN from CloudFormation stack outputs:")
		return a.waitForAWSRoleArn, nil
	}

	fmt.Fprint(out, "✅ AWS authentication configured successfully!\n")
	fmt.Fprintf(out, "   Role: %s\n", roleArn)
	fmt.Fprintf(out, "   Region: %s\n\n", a.awsRegion)

	// Continue to next state (detection or deployment)
	if a.nextStateAfterAuth != nil {
		return a.nextStateAfterAuth(ctx, input, out)
	}
	return a.executeDeployment(ctx, input, out)
}

func (a *Agent) performOAuthLogin(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	fmt.Fprint(out, "🚀 Starting authentication flow...\n")

	// Perform OAuth login using the auth package
	if err := a.auth.PerformOAuthLogin(ctx); err != nil {
		fmt.Fprintf(out, "❌ Authentication failed: %v\n", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"component": "agent",
			"operation": "oauth_login",
			"auth_type": "oauth",
			"platform":  a.DeployPlan.Platform.String(),
			"flow":      "deployment",
		})
		fmt.Fprint(out, "🔧 You can try option 2 (Manual API key setup) instead\n")
		return a.waitForAuthSelection, nil
	}

	fmt.Fprint(out, "✅ Authentication successful!\n")

	// Continue to next state (detection or deployment)
	if a.nextStateAfterAuth != nil {
		return a.nextStateAfterAuth(ctx, input, out)
	}
	return a.executeDeployment(ctx, input, out)
}

func (a *Agent) ensureAuthenticated(ctx context.Context, out io.Writer) bool {
	if !a.internalAuth.IsAuthenticated() {
		fmt.Fprint(out, "🔐 Before we proceed, let's get you logged in!\n")
		authenticated := a.authenticateCLI(ctx)
		if !authenticated {
			fmt.Fprint(out, "❌ Authentication failed. Please try again.\n")
			return false
		}
	}
	return true
}

func (a *Agent) authenticateCLI(ctx context.Context) bool {
	err := a.internalAuth.LoginWithBrowser(ctx)
	if err != nil {
		slog.Error("Failed to authenticate CLI", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"component": "authentication",
			"operation": "supabase_login",
			"auth_type": "cli",
		})
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
		return errors.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}
