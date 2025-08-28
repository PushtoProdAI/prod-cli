package tui

import tea "github.com/charmbracelet/bubbletea"

// AuthOption represents an authentication option
// This is defined here to avoid import cycles, but matches agent.AuthOption
type AuthOption struct {
	Label string
	Mode  string
}

type UIMessage struct {
	Content string
}

func (m UIMessage) String() string {
	return m.Content
}

type ConfirmationPrompt struct {
	Message string
}

func (c ConfirmationPrompt) String() string {
	return c.Message
}

type SpinnerStartMsg struct {
	Message string
}

func (s SpinnerStartMsg) String() string {
	return s.Message
}

type SpinnerStopMsg struct{}

func (s SpinnerStopMsg) String() string {
	return "spinner stop"
}

type AuthSelectionPrompt struct {
	Message string
	Options []AuthOption
}

func (a AuthSelectionPrompt) String() string {
	return a.Message
}

type APIKeyPrompt struct {
	Message string
}

func (a APIKeyPrompt) String() string {
	return a.Message
}

type SelectPrompt struct {
	Message string
	Options []string
	Cursor  int
}

func (s SelectPrompt) String() string {
	return s.Message
}

type TextPrompt struct {
	Message      string
	DefaultValue string
}

func (t TextPrompt) String() string {
	return t.Message
}

// PlanDisplayMessage represents a deployment plan with structured data for table display
type PlanDisplayMessage struct {
	Summary  string
	Action   string
	Platform string
	Source   string
	Name     string
	Language string
	DryRun   bool
	Services []ServiceRequirement
	EnvVars  []EnvVarRequirement
	Routes   []RouteRequirement
	Pricing  PricingInfo
}

type PricingInfo struct {
	Services []PricingService
	Total    float64
}

type PricingService struct {
	Name    string
	Plan    string
	Storage int
	Cost    float64
}

type ServiceRequirement struct {
	Type     string
	Provider string
}

type EnvVarRequirement struct {
	Name string
}

type RouteRequirement struct {
	Path string
}

func (p PlanDisplayMessage) String() string {
	return "Plan Display"
}

var _ tea.Msg = UIMessage{}
var _ tea.Msg = ConfirmationPrompt{}
var _ tea.Msg = SpinnerStartMsg{}
var _ tea.Msg = SpinnerStopMsg{}
var _ tea.Msg = AuthSelectionPrompt{}
var _ tea.Msg = APIKeyPrompt{}
var _ tea.Msg = SelectPrompt{}
var _ tea.Msg = TextPrompt{}
var _ tea.Msg = PlanDisplayMessage{}
