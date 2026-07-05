package tui

import tea "github.com/charmbracelet/bubbletea/v2"

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
	Masked       bool // hide input (sensitive values like secrets/tokens)
}

func (t TextPrompt) String() string {
	return t.Message
}

// PlanDisplayMessage represents a deployment plan with structured data for table display
type PlanDisplayMessage struct {
	Summary           string
	Action            string
	Platform          string
	Source            string
	Name              string
	Language          string
	Services          []ServiceRequirement
	EnvVars           []EnvVarRequirement
	Routes            []RouteRequirement
	Pricing           PricingInfo
	DetectedPlatforms []string
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

// ClipboardCopyMsg represents a message sent when text is copied to clipboard
type ClipboardCopyMsg struct {
	Success bool
	Content string
	Error   string
}

func (c ClipboardCopyMsg) String() string {
	if c.Success {
		return "Copied to clipboard"
	}
	return "Failed to copy: " + c.Error
}

type ErrorDisplayMessage struct {
	Summary      string
	Remediations []RemediationItem
}

type WarningDisplayMessage struct {
	Summary      string
	Remediations []RemediationItem
}

type RemediationItem struct {
	Description string
	CliCommand  string
}

func (e ErrorDisplayMessage) String() string {
	return "Error Display"
}

func (w WarningDisplayMessage) String() string {
	return "Warning Display"
}

type SuccessDisplayMessage struct {
	Platform string
	AppName  string
	Url      string
}

func (s SuccessDisplayMessage) String() string {
	return "Success Display"
}

type InfoBoxMessage struct {
	Title   string
	Content string
	Icon    string
}

func (i InfoBoxMessage) String() string {
	return "Info Box"
}

type ClearScreenMsg struct{}

func (c ClearScreenMsg) String() string {
	return "Clear screen"
}

type QuitMsg struct{}

func (q QuitMsg) String() string {
	return "Quit"
}

type SearchMsg struct{}

func (s SearchMsg) String() string {
	return "Search"
}

// DeploymentHistoryDisplayMessage represents deployment history for table display
type DeploymentHistoryDisplayMessage struct {
	Deployments []DeploymentHistoryEntry
}

type DeploymentHistoryEntry struct {
	OperationID   string
	ResourceName  string
	OperationType string
	Status        string
	Platform      string
	Language      string
	StartedAt     string
	CompletedAt   string
	Duration      int
}

func (d DeploymentHistoryDisplayMessage) String() string {
	return "Deployment History Display"
}

var (
	_ tea.Msg = UIMessage{}
	_ tea.Msg = ConfirmationPrompt{}
	_ tea.Msg = SpinnerStartMsg{}
	_ tea.Msg = SpinnerStopMsg{}
	_ tea.Msg = AuthSelectionPrompt{}
	_ tea.Msg = APIKeyPrompt{}
	_ tea.Msg = SelectPrompt{}
	_ tea.Msg = TextPrompt{}
	_ tea.Msg = PlanDisplayMessage{}
	_ tea.Msg = ClipboardCopyMsg{}
	_ tea.Msg = ErrorDisplayMessage{}
	_ tea.Msg = WarningDisplayMessage{}
	_ tea.Msg = SuccessDisplayMessage{}
	_ tea.Msg = InfoBoxMessage{}
	_ tea.Msg = ClearScreenMsg{}
	_ tea.Msg = QuitMsg{}
	_ tea.Msg = SearchMsg{}
	_ tea.Msg = DeploymentHistoryDisplayMessage{}
)
