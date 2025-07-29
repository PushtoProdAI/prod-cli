package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/go-errors/errors"
	"github.com/manifoldco/promptui"
	"github.com/meroxa/prod/cli/internal/analyzer"
)

type Agent struct {
	sm          deploySM
	wfClient    *client.Client
	interactive bool
	deployPlan  *deployPlan
}

func NewAgent(wfClient *client.Client, interactive bool) *Agent {
	a := &Agent{wfClient: wfClient, interactive: interactive}
	sm := deploySM{currentState: a.plan}
	a.sm = sm
	return a
}

type deployPlan struct {
	Action   Action
	Platform Platform
	Source   string
	Spec     analyzer.ProjectSpec
	Summary  string
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

	fmt.Fprintf(out, "%s\n", plan.Summary)
	fmt.Fprint(out, "-------\n")
	fmt.Fprintf(out, "Action: %s\n", plan.Action)
	fmt.Fprintf(out, "Platform: %s\n", plan.Platform)
	fmt.Fprintf(out, "Source: %s\n", plan.Source)
	fmt.Fprintf(out, "Name: %s\n", plan.Spec.Name)
	fmt.Fprintf(out, "Language: %s\n", plan.Spec.Language)
	fmt.Fprint(out, "-------\n")

	if !shouldProceed(plan) {
		return a.plan, nil
	}
	a.deployPlan = &plan
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
	wf, err := Workflows{}.Deploy(ctx, a.wfClient, *a.deployPlan)
	if err != nil {
		log.Printf("Workflow execution result: %v\n", err)
		fmt.Fprint(out, "Sorry, couldn't create a deployment plan \n")
		return a.plan, nil
	}

	// give a generous timeout for the deployment to complete
	_, err = client.GetWorkflowResult[deployPlan](ctx, a.wfClient, wf, 10*time.Minute)
	if err != nil {
		a.wfClient.CancelWorkflowInstance(ctx, wf)
		fmt.Fprint(out, "Sorry, we had trouble deploying your project \n")
		return a.plan, nil
	}

	io.WriteString(out, "Deployed...\n")
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
