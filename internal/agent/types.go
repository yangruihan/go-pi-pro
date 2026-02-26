package agent

import "context"

type LLM interface {
	Ask(ctx context.Context, prompt string) (string, error)
}

type Approver func(ctx context.Context, step PlanStep) (bool, error)

type RunnerOptions struct {
	MaxActRetries int
	Approver      Approver
	AuditDir      string
}

type PlanStep struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Reason           string `json:"reason"`
	Risk             string `json:"risk"`
	RequiresApproval bool   `json:"requires_approval"`
}

type Plan struct {
	Goal  string     `json:"goal"`
	Steps []PlanStep `json:"steps"`
}

type ActionStepLog struct {
	StepID    string
	Title     string
	Status    string
	Attempts  int
	Output    string
	ErrorText string
}

type StepResult struct {
	ReadSummary string
	Plan        Plan
	ActionLogs  []ActionStepLog
	Final       string
	AuditPath   string
}
