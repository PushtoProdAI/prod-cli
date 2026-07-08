// Package newcmd implements `prod new <template> [name]` — scaffold a starter project that
// prod can deploy. Templates are embedded in the binary (single-binary promise: no network,
// deterministic, offline). Each declares a deploy shape so `prod "deploy this"` classifies it
// correctly even from code alone.
package newcmd

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
)

var projectNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// tmpl is the metadata for a starter. The files live under templates/<name>/; this carries
// what the scaffolder needs to print honest next steps (the shape drives the suggested prompt).
type tmpl struct {
	name        string
	description string
	shape       string // web | mcp-server | worker | cron — informational + drives the prompt
	prompt      string // the suggested `prod "…"` deploy command
}

// templates is the in-code registry (kept next to the embedded files). Add an entry here when
// you add a templates/<name>/ directory.
var templates = []tmpl{
	{
		name:        "agent-worker",
		description: "A LangGraph agent that runs as a background worker (no HTTP server).",
		shape:       "worker",
		prompt:      `prod "deploy this worker to fly"`,
	},
}

func lookupTemplate(name string) (tmpl, bool) {
	for _, t := range templates {
		if t.name == name {
			return t, true
		}
	}
	return tmpl{}, false
}

// NewCommand scaffolds a starter project from an embedded template.
type NewCommand struct {
	template string
	name     string
}

func (c *NewCommand) Usage() string { return "new" }

func (c *NewCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Scaffold a starter project you can deploy",
		Long: "Create a working starter in ./<name>/ from a template, then deploy it with a sentence.\n\n" +
			"  prod new agent-worker my-agent\n" +
			"  cd my-agent && prod \"deploy this worker to fly\"\n\n" +
			"Run `prod new` with no template to list what's available. Templates are embedded in\n" +
			"the binary and each declares its deploy shape, so `prod \"deploy this\"` classifies it\n" +
			"correctly (a worker gets no HTTP health check; an mcp-server gets the handshake).",
	}
}

func (c *NewCommand) Args(args []string) error {
	if len(args) >= 1 {
		c.template = args[0]
	}
	if len(args) >= 2 {
		c.name = args[1]
	}
	if len(args) > 2 {
		return errors.New("new takes at most two arguments: `prod new <template> [name]`")
	}
	return nil
}

func (c *NewCommand) Execute(context.Context) error {
	if c.template == "" {
		return errors.New(availableTemplates("Pick a template:"))
	}
	t, ok := lookupTemplate(c.template)
	if !ok {
		return errors.New(availableTemplates(fmt.Sprintf("Unknown template %q.", c.template)))
	}

	// Default the project name to the template name.
	name := strings.ToLower(strings.TrimSpace(c.name))
	if name == "" {
		name = t.name
	}
	if !projectNameRE.MatchString(name) {
		return errors.Errorf("invalid project name %q: use lowercase letters, digits, and hyphens, starting with a letter", name)
	}
	if _, err := os.Stat(name); err == nil {
		return errors.Errorf("./%s already exists — pick another name or remove it first", name)
	}

	if err := scaffold(t.name, name); err != nil {
		return err
	}

	fmt.Printf(`Created ./%s/  (%s)

Next steps:
  cd %s
  cp .env.example .env      # fill in any required values
  %s

prod detects this as a %q deploy — no HTTP health check, no URL; it just runs.
`, name, t.description, name, t.prompt, t.shape)
	return nil
}

// scaffold copies the embedded templates/<template>/ tree into ./<name>/, expanding {{.Name}}
// in each file's contents.
func scaffold(templateName, projectName string) error {
	root := "templates/" + templateName
	data := struct{ Name string }{Name: projectName}

	return fs.WalkDir(templatesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(projectName, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		raw, err := fs.ReadFile(templatesFS, path)
		if err != nil {
			return errors.Errorf("failed to read template file %s: %w", rel, err)
		}
		rendered, err := render(rel, raw, data)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, rendered, 0o644); err != nil {
			return errors.Errorf("failed to write %s: %w", rel, err)
		}
		return nil
	})
}

func render(name string, raw []byte, data any) ([]byte, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, errors.Errorf("template %s is malformed: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, errors.Errorf("failed to render %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func availableTemplates(prefix string) string {
	names := make([]string, 0, len(templates))
	byName := make(map[string]string, len(templates))
	for _, t := range templates {
		names = append(names, t.name)
		byName[t.name] = t.description
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(prefix + "\n\nAvailable templates:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  %-14s %s\n", n, byName[n])
	}
	b.WriteString("\nUsage: prod new <template> [name]")
	return b.String()
}
