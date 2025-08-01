package root

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chzyer/readline"
	"github.com/conduitio/ecdysis"
	"github.com/meroxa/prod/cli/internal/agent"
)

var (
	_ ecdysis.CommandWithFlags   = (*RootCommand)(nil)
	_ ecdysis.CommandWithExecute = (*RootCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*RootCommand)(nil)
	_ ecdysis.CommandWithOutput  = (*RootCommand)(nil)
	_ ecdysis.CommandWithArgs    = (*RootCommand)(nil)
)

const exitPrompt = "exit"

type RootFlags struct {
	DryRun  bool `long:"dry-run" usage:"simulate the execution without making any changes"`
	Version bool `long:"version" short:"v" usage:"show the current Prod version"`
}

type RootArgs struct {
	prompt string
}

type RootCommand struct {
	flags  RootFlags
	args   RootArgs
	output ecdysis.Output
	Agent  *agent.Agent
}

func (c *RootCommand) Args(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("too many arguments")
	}

	if len(args) == 1 {
		c.args.prompt = args[0]
	}

	return nil
}

func (c *RootCommand) Output(output ecdysis.Output) {
	c.output = output
}

func (c *RootCommand) Execute(ctx context.Context) error {
	const banner = `
______              _ 
| ___ \            | |
| |_/ / __ ___   __| |
|  __/ '__/ _ \ / _` + "`" + ` |
| |  | | | (_) | (_| |
\_|  |_|  \___/ \__,_|
`

	if c.flags.Version {
		c.output.Stdout(fmt.Sprintf("%s\n", "0.0.1"))
		return nil
	}

	c.output.Stdout(fmt.Sprintf("%s\n", banner))

	if c.flags.DryRun {
		c.output.Stdout("🔍 DRY RUN MODE - Simulating execution without making changes\n\n")
	}

	if c.args.prompt != "" {
		c.processPrompt(c.args.prompt)
		return nil
	}

	// Create a context that can be canceled on SIGINT (Ctrl+C)
	ctxWithCancel, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	c.output.Stdout(fmt.Sprintf("Type %q or press Ctrl+C to exit.\n", exitPrompt))
	c.output.Stdout(greetUser() + "\n")
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     "/tmp/.prodcli_app_history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize readline: %w", err)
	}
	defer rl.Close()

	for {
		select {
		case <-ctxWithCancel.Done():
			c.output.Stdout("\nInterrupted, exiting...\n")
			return nil
		default:
		}

		line, err := rl.Readline()
		switch err {
		case nil:
		case readline.ErrInterrupt:
			c.output.Stdout("\nInterrupted, exiting...\n")
			return nil
		case io.EOF:
			c.output.Stdout("\nEOF detected, exiting...\n")
			return nil
		default:
			return fmt.Errorf("readline error: %w", err)
		}
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if line == exitPrompt {
			c.output.Stdout("Exiting...\n")
			return nil
		}

		c.processPrompt(line)
	}
}

// processPrompt handles the business logic for processing prompts
// This method is called both when a prompt is provided as an argument
// and when input is captured from interactive mode
func (c *RootCommand) processPrompt(prompt string) {
	ctx := context.Background()
	c.Agent.SetDryRun(c.flags.DryRun)
	c.Agent.Process(ctx, prompt, c)
}

func (c *RootCommand) Usage() string { return "prod" }

func (c *RootCommand) Flags() []ecdysis.Flag {
	return ecdysis.BuildFlags(&c.flags)
}

func (c *RootCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Prod",
		Long:  `Prod starts an interactive session by default.`,
	}
}

func (c *RootCommand) Write(p []byte) (n int, err error) {
	if c.output != nil {
		c.output.Stdout(string(p))
		return len(p), nil
	}
	return 0, fmt.Errorf("output not set")
}

func greetUser() string {
	prompts := []string{
		"What would you like to deploy today?",
		"Ready to launch something new?",
		"What’s next on your cloud adventure?",
		"Need a hand with your app or infra today?",
		"What’s cooking—deployments, logs, or maybe scaling?",
		"What can I help you ship today?",
		"How can I make your cloud life easier?",
		"Working on something exciting? Let's get it live.",
		"Want to check on a service, deploy something, or try something new?",
		"Let’s turn code into something live—what’s the plan?",
		"Your cloud assistant is ready. What’s on the agenda?",
		"Deploy. Debug. Discover. What’s your move?",
		"One terminal. Infinite possibility. What shall we do?",
		"Just me and you—what should we take care of today?",
		"Looking to deploy, inspect, or tweak something?",
		"Need insights, deployments, or just a friend in the cloud?",
		"What mission are we embarking on today?",
		"Want to push some code or peek under the hood?",
		"Cloud control is yours. What’s first?",
		"I’m all ears (and APIs). What’s the task?",
	}

	prompt := prompts[rand.Intn(len(prompts))]

	return prompt
}
