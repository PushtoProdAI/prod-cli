package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/backend"
	"github.com/pushtoprodai/prod-cli/internal/config"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
	"github.com/pushtoprodai/prod-cli/internal/llm"
	"github.com/pushtoprodai/prod-cli/internal/output"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
	"golang.org/x/term"
)

type TUIWriter interface {
	io.Writer
	SendConfirmation(message string, callback func(bool))
	SendAPIKeyPrompt(message string)
	SendSelect(message string, options []string)
	SendTextPrompt(message string)
	SendTextPromptWithDefault(message string, defaultValue string)
	SendSecretPrompt(message string)
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
	sm                 deploySM
	wfClient           *client.Client
	interactive        bool
	dryRun             bool
	oneShot            bool
	DeployPlan         *DeployPlan
	UIOutput           io.Writer
	auth               auth.AuthProvider
	envVars            []EnvVarWithStatus
	internalAuth       *auth.SupabaseAuth
	nextStateAfterAuth stateFn // State to transition to after successful PaaS authentication
	// awaitingSecret tells the console one-shot driver that the value it is about to
	// read is sensitive, so it reads it masked (no echo) on a real terminal.
	awaitingSecret bool
}

type agentContextKey string

const agentAuthSession agentContextKey = "AuthSession"

func NewAgent(wfClient *client.Client, internalAuth *auth.SupabaseAuth) *Agent {
	a := &Agent{
		wfClient:     wfClient,
		interactive:  true, // Default to interactive
		envVars:      make([]EnvVarWithStatus, 0),
		internalAuth: internalAuth,
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
	Shape               deployment.DeployShape // web (default) | mcp-server | worker | cron
}

// Plan approval response constants
const (
	PlanApprovalApproved = "approved"
	PlanApprovalRejected = "rejected"
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

// done is the terminal transition after a completed action (deploy, rollback,
// error, cancel). In the interactive TUI it returns to checkPrerequisites to
// accept the next command; in a one-shot run (`prod "..."`, `--yes`) it ends the
// state machine so the process exits instead of blocking for more input.
func (a *Agent) done() stateFn {
	if a.oneShot {
		return nil
	}
	return a.checkPrerequisites
}

// stateID identifies the current state so a driver can tell whether a Process
// call made forward progress. Distinct states are distinct methods, so distinct
// code pointers; a state that waits for input returns itself → same pointer.
func (a *Agent) stateID() uintptr {
	if a.sm.currentState == nil {
		return 0
	}
	return reflect.ValueOf(a.sm.currentState).Pointer()
}

// DriveOneShot runs a single prompt to completion on the non-TUI console path.
// The deploy is a multi-state flow but Process advances one state per call, so a
// single call would dead-end at the confirmation prompt. This drives it:
//   - autoApprove (--yes): proceed without prompting, with a no-progress guard so
//     a step that needs input we can't supply (e.g. an env-var value) fails fast
//     instead of spinning.
//   - otherwise: answer prompts (confirmation, env-var values) by reading lines
//     from in until the flow completes or input ends (EOF/non-TTY → stop, never
//     hang), so a plain-terminal `prod "deploy ..."` reaches a URL.
func (a *Agent) DriveOneShot(ctx context.Context, prompt string, out io.Writer, in io.Reader, autoApprove bool) {
	a.interactive = !autoApprove
	a.oneShot = true // terminal states end the machine instead of looping for more commands
	a.Process(ctx, prompt, out)

	if autoApprove {
		for !a.IsComplete() {
			before := a.stateID()
			a.Process(ctx, "", out)
			if !a.IsComplete() && a.stateID() == before {
				fmt.Fprintln(out, "This step needs input that can't be provided with --yes. "+
					"Run it in a terminal, or set the values (e.g. environment variables) in your environment.")
				return
			}
		}
		return
	}

	for !a.IsComplete() {
		line, ok := a.readOneShotLine(out, in)
		if !ok {
			return // EOF / non-interactive input: stop instead of hanging
		}
		a.Process(ctx, line, out)
	}
}

// readOneShotLine reads the next answer for the console one-shot driver. When the
// agent is awaiting a sensitive value and the input is a real terminal, it reads
// masked (no echo) via term.ReadPassword; otherwise it reads a plain line. It
// returns ok=false to signal "stop" (EOF with nothing buffered, or a masked read
// error), preserving the driver's "never hang" behavior.
//
// It deliberately avoids a persistent buffered reader: readLine consumes exactly
// up to the newline with no read-ahead, so switching to term.ReadPassword on the
// same fd for a sensitive line cannot lose bytes a buffer had already swallowed.
func (a *Agent) readOneShotLine(out io.Writer, in io.Reader) (string, bool) {
	if a.awaitingSecret {
		// Only a real *os.File terminal can be masked. A pipe/redirect can't be
		// masked (and often is EOF), so fall through to the normal line read.
		if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			b, err := term.ReadPassword(int(f.Fd()))
			if err != nil {
				return "", false
			}
			// ReadPassword consumes the Enter keystroke without echoing it, so the
			// cursor stays on the prompt line; emit the newline ourselves.
			fmt.Fprintln(out)
			return string(b), true
		}
	}

	line, err := readLine(in)
	if err != nil && line == "" {
		return "", false // EOF with nothing buffered: stop instead of hanging
	}
	return line, true
}

// readLine reads a single line from r one byte at a time (no read-ahead), so a
// later masked read on the same fd loses no bytes. The trailing '\n' is stripped;
// a trailing '\r' (CRLF) is trimmed. A final line without a newline is returned
// together with the underlying error (e.g. io.EOF).
func readLine(r io.Reader) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimSuffix(sb.String(), "\r"), nil
			}
			sb.WriteByte(buf[0])
		}
		if err != nil {
			return strings.TrimSuffix(sb.String(), "\r"), err
		}
	}
}

// SetDryRun makes a deploy stop after showing the plan (and cost), touching
// nothing in the cloud.
func (a *Agent) SetDryRun(dryRun bool) {
	a.dryRun = dryRun
}

// IsComplete returns true if the state machine has finished (no current state)
func (a *Agent) IsComplete() bool {
	return a.sm.currentState == nil
}

// isJSONMode checks if the agent is running in JSON mode (VSCode extension integration)
func (a *Agent) isJSONMode() bool {
	return os.Getenv("PROD_JSON_MODE") == "true"
}

// Helper methods for TUI operations. Each degrades to a plain-text render on a
// non-TUI writer (console/JSON) instead of panicking on the type assertion.
func (a *Agent) sendPlan(out io.Writer, plan DeployPlan) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendPlan(plan)
		return
	}
	// Console fallback: include shape and estimated cost so a decision to spend
	// money is made with the numbers in view (the TUI renders these in its card).
	fmt.Fprintf(out, "\nPlan: %s to %s — %s\n", plan.Action.String(), plan.Platform.String(), plan.Summary)
	// Only surface a non-default shape — "web" is the norm and would just be noise.
	if plan.Shape != "" && plan.Shape != deployment.ShapeWeb {
		fmt.Fprintf(out, "  Shape: %s\n", plan.Shape)
	}
	if plan.Pricing.Total > 0 {
		fmt.Fprintf(out, "  Estimated cost: ~$%.2f/mo\n", plan.Pricing.Total)
	}
}

func (a *Agent) sendPlanApprovalRequest(out io.Writer, plan DeployPlan) {
	// Build plan details for JSON output
	planData := map[string]interface{}{
		"action":   plan.Action.String(),
		"platform": plan.Platform.String(),
		"summary":  plan.Summary,
		"shape":    plan.Shape.String(),
		"project": map[string]interface{}{
			"name":     plan.Spec.Name,
			"language": plan.Spec.Language,
			"source":   plan.Source,
		},
	}

	// Add pricing if available
	if plan.Pricing.Total > 0 {
		planData["pricing"] = map[string]interface{}{
			"total":    plan.Pricing.Total,
			"services": plan.Pricing.Services,
		}
	}

	// Emit through StatusWriter interface
	if statusWriter, ok := out.(output.StatusWriter); ok {
		statusWriter.SendPlanApprovalRequest(planData)
	}
}

func (a *Agent) waitForPlanApproval(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	input = strings.ToLower(strings.TrimSpace(input))

	switch input {
	case PlanApprovalApproved:
		fmt.Fprint(out, "Proceeding with deployment...\n")
		a.nextStateAfterAuth = a.detectExisting
		return a.checkAuthentication(ctx, input, out)
	case PlanApprovalRejected:
		fmt.Fprint(out, "Deployment cancelled\n")
		return nil, nil
	default:
		// Invalid response, stay in this state and wait for valid input
		slog.Info("Invalid plan approval response", "input", input)
		return a.waitForPlanApproval, nil
	}
}

func (a *Agent) sendConfirmation(out io.Writer, message string) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendConfirmation(message, nil)
		return
	}
	fmt.Fprintf(out, "%s\n", message)
}

func (a *Agent) sendSelect(out io.Writer, message string, options []string) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendSelect(message, options)
		return
	}
	fmt.Fprintf(out, "%s\n", message)
	for i, opt := range options {
		fmt.Fprintf(out, "  %d. %s\n", i+1, opt)
	}
}

func (a *Agent) sendAPIKeyPrompt(out io.Writer, message string) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendAPIKeyPrompt(message)
		return
	}
	fmt.Fprintf(out, "%s\n", message)
}

func (a *Agent) sendTextPrompt(out io.Writer, message string) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendTextPrompt(message)
		return
	}
	fmt.Fprintf(out, "%s\n", message)
}

// sendSecretPrompt prompts for a sensitive value; the TUI masks input as it's
// typed. (Console input isn't masked yet — see the env-var prompt.)
func (a *Agent) sendSecretPrompt(out io.Writer, message string) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendSecretPrompt(message)
		return
	}
	fmt.Fprintf(out, "%s\n", message)
}

func (a *Agent) sendTextPromptWithDefault(out io.Writer, message string, defaultValue string) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.SendTextPromptWithDefault(message, defaultValue)
		return
	}
	fmt.Fprintf(out, "%s [%s]\n", message, defaultValue)
}

func (a *Agent) stopSpinner(out io.Writer) {
	if tuiWriter, ok := out.(TUIWriter); ok {
		tuiWriter.StopSpinner()
	}
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
	if a.internalAuth != nil && a.internalAuth.IsAuthenticated() {
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

	// Always check authentication when checkPrerequisites is called
	// This ensures auth is validated on every new user input
	if !a.ensureAuthenticated(ctx, out) {
		return a.done(), nil
	}

	// Get session and enrich context after successful authentication
	session, err := a.internalAuth.GetSession()
	if err != nil {
		slog.Error("Failed to get session", "error", err)
	}
	ctxWithSession := WithCtxSession(ctx, session)

	// Auth is complete, proceed to plan
	return a.plan(ctxWithSession, input, out)
}

func (a *Agent) handleSlashCommand(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return a.done(), nil
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
	return a.done(), nil
}

func (a *Agent) plan(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	// Preflight: prod needs an LLM to plan. Cloud keys are trusted if present; the
	// local Ollama fallback is probed, so a keyless user without Ollama gets a clear
	// message here instead of a raw connection error mid-plan.
	if p := llm.Detect(os.Getenv); !p.Ready {
		fmt.Fprintf(out, "prod needs an LLM to plan a deploy, but %s.\n"+
			"Set OPENAI_API_KEY or ANTHROPIC_API_KEY, or run a local Ollama (https://ollama.com).\n"+
			"Run `prod doctor` to check your setup.\n", p.Detail)
		if !a.interactive {
			return nil, nil
		}
		return a.done(), nil
	}

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

	// "deploy this" with no platform named is the most natural command a newcomer
	// types. Prompt for one (interactive) or give a clear, actionable error instead
	// of dead-ending in shouldProceed.
	if plan.Action == Deploy && plan.Platform == UnknownPlatform {
		return a.selectDeployPlatform(ctx, plan, input, out)
	}

	return a.proceedWithPlan(ctx, plan, input, out)
}

// deployPlatforms is the menu's platform order, derived from the catalog.
func deployPlatforms() []Platform {
	specs := RegisteredPlatforms()
	ps := make([]Platform, len(specs))
	for i, s := range specs {
		ps[i] = s.Platform
	}
	return ps
}

// deployPlatformNames is the menu's labels — the catalog's display names (e.g.
// "Google Cloud Run", not the enum's "GoogleCloudRun").
func deployPlatformNames() []string {
	specs := RegisteredPlatforms()
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

// parseDeployPlatform accepts a menu index (0-based, the TUI select convention) or
// a platform name/alias (so a user can type "fly" or "cloud run").
func parseDeployPlatform(input string) Platform {
	s := strings.TrimSpace(input)
	specs := RegisteredPlatforms()
	var idx int
	if _, err := fmt.Sscanf(s, "%d", &idx); err == nil && idx >= 0 && idx < len(specs) {
		return specs[idx].Platform
	}
	if p, ok := PlatformByString(s); ok {
		return p
	}
	return UnknownPlatform
}

func (a *Agent) selectDeployPlatform(_ context.Context, plan DeployPlan, _ string, out io.Writer) (stateFn, error) {
	if !a.interactive || a.isJSONMode() {
		fmt.Fprint(out, "I couldn't tell which platform to deploy to. Add one to your prompt, e.g.:\n"+
			"  prod \"deploy this to fly\"\n"+
			"Supported: Fly.io, Render, Vercel, Netlify, Heroku, AWS.\n")
		return a.done(), nil
	}
	a.DeployPlan = &plan // stash; the selection fills in the platform
	a.sendSelect(out, "Which platform should I deploy to?", deployPlatformNames())
	return a.waitForDeployPlatformSelection, nil
}

func (a *Agent) waitForDeployPlatformSelection(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	p := parseDeployPlatform(input)
	if p == UnknownPlatform {
		a.sendSelect(out, "Please pick a platform:", deployPlatformNames())
		return a.waitForDeployPlatformSelection, nil
	}
	a.DeployPlan.Platform = p
	fmt.Fprintf(out, "Deploying to %s.\n", p.String())
	return a.proceedWithPlan(ctx, *a.DeployPlan, input, out)
}

// proceedWithPlan runs the shared post-plan gate + approval dispatch, used both
// for a fully-specified plan and after a platform is chosen interactively.
func (a *Agent) proceedWithPlan(ctx context.Context, plan DeployPlan, input string, out io.Writer) (stateFn, error) {
	if !shouldProceed(plan) {
		fmt.Fprintf(out, "Cannot proceed with deployment plan\n")
		return a.done(), nil
	}

	// Gate the deploy: AWS needs the backend (local-refused), Render needs the
	// user's registry. Rollback is gated separately (executeRollback) since a
	// Render rollback needs neither.
	if a.refuseDeployPlatform(out, plan.Platform) {
		return a.done(), nil
	}
	a.DeployPlan = &plan

	// Dry run: show the plan (with shape + cost, via sendPlan) and stop — nothing
	// is created in the cloud.
	if a.dryRun {
		a.sendPlan(out, plan)
		fmt.Fprint(out, "\n🔎 Dry run — nothing was deployed. Re-run without --dry-run to ship it.\n")
		if !a.interactive {
			return nil, nil
		}
		return a.done(), nil
	}

	// Check if we're in JSON mode (VSCode extension integration)
	if a.isJSONMode() {
		// JSON mode: emit plan_approval_request and wait for response
		a.sendPlanApprovalRequest(out, plan)
		return a.waitForPlanApproval, nil
	} else if a.interactive {
		// TUI mode: send plan to TUI and wait for confirmation
		a.sendPlan(out, plan)
		return a.confirmWithPrompt(ctx, input, out)
	}

	// Non-interactive, non-JSON mode: proceed directly
	return a.confirm, nil
}

// confirmMessage frames approval as one decision block — the action, the target,
// and (for a deploy) the estimated cost — so y/N isn't asked in the abstract right
// after a plan the user has to scroll back to.
func (a *Agent) confirmMessage() string {
	p := a.DeployPlan
	if p == nil {
		return "Do you want to proceed?"
	}
	verb := "Deploy"
	if p.Action == Rollback {
		verb = "Roll back"
	}
	name := p.Spec.Name
	if name == "" {
		name = "this project"
	}
	msg := fmt.Sprintf("%s %s to %s", verb, name, p.Platform.String())
	if p.Action == Deploy && p.Pricing.Total > 0 {
		msg += fmt.Sprintf(" (~$%.2f/mo)", p.Pricing.Total)
	}
	return msg + "?"
}

func (a *Agent) confirmWithPrompt(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	// Check if this is the initial call or a response to confirmation
	if input == "" {
		// Initial call - send confirmation prompt
		a.sendConfirmation(out, a.confirmMessage())
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
		return a.done(), nil
	}

	// Invalid response - ask again
	fmt.Fprintf(out, "Please answer y or n.\n")
	a.sendConfirmation(out, a.confirmMessage())
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
		return a.done(), nil
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
		return a.done(), nil
	}

	a.DeployPlan.ExistingProjectInfo = result

	var summaryText string
	if result.Exists {
		summaryText = "🔍 Existing Resources Found:\n\n"
		summaryText += fmt.Sprintf("• Application: %s (will be updated)\n", result.Name)

		if len(result.ExistingDatabases) > 0 {
			summaryText += "\n• Backing Services (will be reused):\n"
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
				summaryText += fmt.Sprintf("• %s\n", service)
			}
		}
	} else {
		summaryText = "📦 New Deployment:\n\n"
		summaryText += fmt.Sprintf("• Application: %s (new)\n", a.DeployPlan.Spec.Name)

		// Count only actual infrastructure resources (database, cache)
		backingServices := []string{}
		for _, service := range a.DeployPlan.Spec.ServiceRequirements {
			// Only include actual infrastructure resources
			if service.Type != "database" && service.Type != "cache" {
				continue
			}
			backingServices = append(backingServices, service.Provider)
		}

		if len(backingServices) > 0 {
			summaryText += "\n• Backing Services (new):\n"
			for _, svc := range backingServices {
				summaryText += fmt.Sprintf("  - %s\n", svc)
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

// isFrameworkManagedVar checks if a variable is managed by a framework handler
// Framework handlers will set these values in PrepareDeployment, so don't prompt user
func isFrameworkManagedVar(varName string, spec analyzer.ProjectSpec) bool {
	// Check if this is a Django project
	for _, sr := range spec.ServiceRequirements {
		if sr.Type == "framework" && sr.Provider == "django" {
			// Django framework vars
			if strings.Contains(varName, "ALLOWED_HOSTS") ||
				strings.Contains(varName, "CSRF_TRUSTED_ORIGINS") {
				return true
			}
		}
		// Add other frameworks here as needed
		// if sr.Provider == "rails" { ... }
	}
	return false
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
			// Check if this is a framework-managed var (Django, Rails, etc.)
			// These will be set by PrepareDeployment, so don't prompt the user
			// and don't add to a.envVars (framework handles them completely)
			if isFrameworkManagedVar(envVar.Name, a.DeployPlan.Spec) {
				// Show in auto-populated list but don't add to a.envVars
				if envVar.Sensitive {
					sensitiveAutoPopulated = append(sensitiveAutoPopulated, envVar.Name)
				} else {
					nonSensitiveAutoPopulated = append(nonSensitiveAutoPopulated, envVar.Name)
				}
				// Skip adding to a.envVars - framework will add to CollectedEnvVars directly
				continue
			} else {
				// Application-level vars need user input (unless .env provides defaults)
				a.envVars = append(a.envVars, EnvVarWithStatus{
					EnvVar: envVar,
					Status: "pending",
				})
				pendingCount++
				if envVar.Sensitive {
					sensitivePending = append(sensitivePending, envVar.Name)
				}
			}
		} else {
			// Backing service vars (DB, Redis) - deployment system will auto-populate values
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
			fmt.Fprintf(out, "🔒 marks sensitive values. prod stores them as encrypted secrets on the platform — never in plain config. (Masked as you type in the interactive UI.)\n")
		}
		return a.promptForEnvVarValue(ctx, input, out)
	}

	// All env vars are database-related or already have values, proceed with language-specific preparation
	fmt.Fprintf(out, "✅ All environment variables are ready. Proceeding to project preparation...\n")
	return a.prepareProject(ctx, input, out)
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
		// No more pending env vars, proceed with language-specific preparation.
		// Clear the secret flag in case the last var read was sensitive, so the
		// driver doesn't treat unrelated later input as masked.
		a.awaitingSecret = false
		fmt.Fprintf(out, "All environment variable values collected. Proceeding to project preparation...\n")
		return a.prepareProject(ctx, input, out)
	}

	// Check if we're in JSON mode
	if a.isJSONMode() {
		// JSON mode: emit env_var_prompt event through StatusWriter
		promptMessage := fmt.Sprintf("Enter value for environment variable '%s':", currentEnvVar.Name)
		if statusWriter, ok := out.(output.StatusWriter); ok {
			statusWriter.SendEnvVarPrompt(currentEnvVar.Name, currentEnvVar.Value, promptMessage)
		}
		return a.waitForEnvVarValue, nil
	}

	// TUI mode: use text prompt
	promptMessage := fmt.Sprintf("Enter value for environment variable '%s':", currentEnvVar.Name)
	// Tell the console one-shot driver whether the value it's about to read is
	// sensitive, so it can read it masked on a real terminal (the TUI masks via
	// its own SendSecretPrompt widget; this flag drives the plain-terminal path).
	a.awaitingSecret = currentEnvVar.Sensitive
	switch {
	case currentEnvVar.Sensitive:
		// Mask secrets/tokens as they're typed (TUI).
		a.sendSecretPrompt(out, promptMessage)
	case currentEnvVar.Value != "":
		// Pre-fill the input with the detected default value.
		a.sendTextPromptWithDefault(out, promptMessage, currentEnvVar.Value)
	default:
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

func (a *Agent) prepareProject(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	// Route to the appropriate language-specific preparation
	switch a.DeployPlan.Spec.Language {
	case "node":
		return a.prepareJS(ctx, input, out)
	case "python":
		return a.preparePython(ctx, input, out)
	default:
		// No language-specific preparation needed, proceed directly to deployment
		return a.deploy(ctx, input, out)
	}
}

// displayDiffLines is a helper function to display diff lines with color formatting
func displayDiffLines(out io.Writer, diffLines []DiffLine) {
	fmt.Fprint(out, "────────────────────────────────────────\n")
	for _, line := range diffLines {
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

// handleSetupError handles errors from setup workflows in a standardized way
func handleSetupError(ctx context.Context, out io.Writer, err deployError, confirmFn func(context.Context, string, io.Writer) (stateFn, error)) (stateFn, error) {
	if err.Summary == "" {
		return nil, nil
	}

	if tuiWriter, ok := out.(TUIWriter); ok {
		if err.IsWarning {
			tuiWriter.SendWarning(err.Summary, err.Remediations)
		} else {
			tuiWriter.SendError(err.Summary, err.Remediations)
		}
	} else {
		if err.IsWarning {
			fmt.Fprintf(out, "⚠️  %s\n", err.Summary)
		} else {
			fmt.Fprintf(out, "❌ %s\n", err.Summary)
		}
		if len(err.Remediations) > 0 {
			fmt.Fprint(out, "Here are some suggestions to fix the issues:\n")
			for _, r := range err.Remediations {
				fmt.Fprintf(out, " • %s\n", r.Description)
				if r.CliCommand != "" {
					fmt.Fprintf(out, "   Run: %s\n", r.CliCommand)
				}
			}
		}
		fmt.Fprint(out, "Once you are ready to retry, just let me know!\n")
	}
	return confirmFn(ctx, "", out)
}

// prepareLanguage is a generic function that handles environment preparation for any language
func (a *Agent) prepareLanguage(
	ctx context.Context,
	input string,
	out io.Writer,
	languageCheck string,
	preparingMsg string,
	successMsg string,
	workflowName string,
	projectType string,
	workflowFn func(context.Context, *client.Client, DeployPlan) (*workflow.Instance, error),
) (stateFn, error) {
	if a.DeployPlan.Spec.Language != languageCheck {
		return a.deploy(ctx, input, out)
	}

	fmt.Fprintf(out, "%s\n", preparingMsg)
	wf, err := workflowFn(ctx, a.wfClient, *a.DeployPlan)
	if err != nil {
		slog.Error("Workflow execution result", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     workflowName,
			"component":    "agent",
			"platform":     a.DeployPlan.Platform,
			"project_type": projectType,
		})
		fmt.Fprint(out, "Sorry, couldn't create a deployment plan \n")
		return a.done(), nil
	}

	result, err := client.GetWorkflowResult[SetupProjectResult](ctx, a.wfClient, wf, 2*time.Minute)
	if err != nil {
		fmt.Fprint(out, "Once you are ready to retry, just let me know!\n")
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     workflowName,
			"component":    "agent",
			"operation":    "get_workflow_result",
			"platform":     a.DeployPlan.Platform,
			"project_name": a.DeployPlan.Spec.Name,
			"language":     a.DeployPlan.Spec.Language,
		})
		return a.confirmWithPrompt(ctx, "", out)
	}

	if nextState, err := handleSetupError(ctx, out, result.Error, a.confirmWithPrompt); nextState != nil || err != nil {
		return nextState, err
	}

	// Display all config changes
	for _, change := range result.ConfigChanges {
		if len(change.Diff) > 0 {
			if change.Icon != "" {
				fmt.Fprintf(out, "\n%s %s changes:\n", change.Icon, change.Name)
			} else {
				fmt.Fprintf(out, "\n%s changes:\n", change.Name)
			}
			displayDiffLines(out, change.Diff)
			if change.ExtraInfo != "" {
				fmt.Fprintf(out, "%s\n", change.ExtraInfo)
			}
		}
	}

	// Display environment variables if any
	if len(result.EnvVars) > 0 {
		fmt.Fprint(out, "\n🔒 Framework environment variables will be set:\n")
		for key, value := range result.EnvVars {
			fmt.Fprintf(out, "  • %s=%s\n", key, value)
		}
		fmt.Fprint(out, "\n")
	}

	a.DeployPlan = &result.UpdatedPlan
	fmt.Fprintf(out, "%s\n", successMsg)

	// After preparation completion, proceed with deployment
	return a.deploy(ctx, input, out)
}

func (a *Agent) prepareJS(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	return a.prepareLanguage(
		ctx, input, out,
		"node",
		"🔧 Preparing JavaScript environment...",
		"✅ JavaScript environment prepared successfully!",
		"setup_javascript_project",
		"javascript",
		Workflows{}.SetupJavaScriptProject,
	)
}

func (a *Agent) preparePython(ctx context.Context, input string, out io.Writer) (stateFn, error) {
	return a.prepareLanguage(
		ctx, input, out,
		"python",
		"🔧 Preparing Python environment...",
		"✅ Python environment prepared successfully!",
		"setup_python_project",
		"python",
		Workflows{}.SetupPythonProject,
	)
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

	// Merge collected env vars from categorization/user input
	// Framework-managed vars are already in DeployPlanWithEnvVars.CollectedEnvVars
	// (added by PrepareDeployment), so we just append the rest
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
		return a.done(), nil
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
		return a.done(), nil
	}

	if result.Error.Summary != "" {
		// deployment_complete event is emitted from planning.go workflow layer
		// Here we just handle UI rendering based on mode
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
		return a.done(), nil
	}

	// deployment_complete event is emitted from planning.go workflow layer
	// Here we just handle UI rendering based on mode
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
		// Make rollback discoverable. (App Runner rollback isn't supported yet.)
		if a.DeployPlan.Platform != AWS {
			io.WriteString(out, "Need to undo this? Run:  prod \"rollback\"\n")
		}
	}

	// In non-interactive mode, end the state machine
	if !a.interactive {
		return nil, nil
	}
	// In interactive mode, return to input processing state for more commands
	return a.done(), nil
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

	// Multi-platform rollback reassigns the platform after the plan-time gate, so
	// re-check here — executeRollback is the single chokepoint for every rollback.
	if a.refuseUnsupportedPlatform(out, a.DeployPlan.Platform) {
		return a.done(), nil
	}

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
		return a.done(), nil
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
		return a.done(), nil
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
		return a.done(), nil
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
	return a.done(), nil
}

// refuseDeployPlatform gates the DEPLOY path. Render requires the user's own
// container registry (there is no hosted push path). AWS deploys to App Runner
// with the user's own credentials, which are validated at the auth step, so it
// needs no gate here.
func (a *Agent) refuseDeployPlatform(out io.Writer, p Platform) bool {
	if p == Render {
		if _, err := prodreg.FromEnv(os.Getenv); err != nil {
			fmt.Fprint(out, "⚠️  Render needs your own container registry. Set PROD_REGISTRY (dockerhub|ghcr|generic)\n"+
				"   plus PROD_REGISTRY_USERNAME and PROD_REGISTRY_TOKEN, then retry.\n")
			return true
		}
	}
	return false
}

// refuseUnsupportedPlatform gates the ROLLBACK path for platforms whose rollback
// isn't available in the single binary (AWS App Runner). Deploy is not gated
// here — a Render or AWS deploy uses the user's own registry/credentials.
func (a *Agent) refuseUnsupportedPlatform(out io.Writer, p Platform) bool {
	if config.BackendConfigured() {
		return false // managed mode: the backend powers rollback
	}
	if msg, unsupported := unsupportedLocalPlatform(p); unsupported {
		fmt.Fprint(out, msg)
		return true
	}
	return false
}

// unsupportedLocalPlatform reports platforms whose ROLLBACK isn't supported in
// the single binary. AWS deploys to App Runner, but App Runner rollback isn't
// implemented yet — you redeploy to change the service — so rollback is refused.
// unsupportedLocalPlatform returns a friendly message (and true) when a platform's
// rollback isn't implemented yet, so the deploy path can refuse cleanly instead of
// failing mid-workflow. Derived from the catalog's SupportsRollback flag.
func unsupportedLocalPlatform(p Platform) (string, bool) {
	s, ok := LookupPlatform(p)
	if !ok || s.SupportsRollback {
		return "", false
	}
	return fmt.Sprintf("⚠️  Rolling back your %s deployment isn't supported yet.\n"+
		"   Deploy again to update the service. (Fly.io, Render, and others support rollback.)\n", s.Name), true
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
	authProvider, err := a.getAuthProvider(out)
	if err != nil {
		fmt.Fprintf(out, "Error getting authentication provider: %v\n", err)
	}

	// Check if already authenticated
	authenticated, err := authProvider.CheckAuthentication(ctx)
	if err != nil {
		fmt.Fprintf(out, "Error checking authentication: %v\n", err)
		return a.done(), err
	}

	if !authenticated {
		// This is the platform's own credentials, not a prod account — prod has none.
		fmt.Fprintf(out, "🔐 Connect your %s account to deploy — prod uses your own credentials.\n\n", a.DeployPlan.Platform)

		// Store the auth provider for use in authentication states
		a.auth = authProvider

		// Check if we're in JSON mode
		if a.isJSONMode() {
			// JSON mode: skip selection and go straight to OAuth login
			return a.performOAuthLogin(ctx, input, out)
		}

		// In non-interactive mode, exit state machine
		if !a.interactive {
			return nil, nil
		}

		// In interactive TUI mode, show selection and transition to auth selection state
		a.sendSelect(out, fmt.Sprintf("How would you like to connect to %s?", a.DeployPlan.Platform), []string{
			"Log in via browser (recommended)",
			"Paste an API token",
		})
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
	p, ok := LookupPlatform(a.DeployPlan.Platform)
	if !ok {
		return nil, errors.Errorf("unsupported platform: %s", a.DeployPlan.Platform)
	}
	return p.NewAuthProvider(out), nil
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
		a.sendSelect(out, "Please choose how to connect:", []string{
			"Log in via browser (recommended)",
			"Paste an API token",
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
	// Local mode has no backend and requires no prod-account login. Deploys run
	// with the user's own platform credentials; there's nothing to sign in to.
	if !config.BackendConfigured() {
		return true
	}
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
