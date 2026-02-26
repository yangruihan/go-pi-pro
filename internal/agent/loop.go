package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/coderyrh/gopi-pro/internal/todo"
)

type Runner struct {
	llm   LLM
	todos *todo.Store
	opts  RunnerOptions
}

func NewRunner(llm LLM, opts RunnerOptions) *Runner {
	if opts.MaxActRetries <= 0 {
		opts.MaxActRetries = 2
	}
	if strings.TrimSpace(opts.AuditDir) == "" {
		opts.AuditDir = filepath.Join(".gopi-pro", "runs")
	}
	return &Runner{llm: llm, todos: todo.New(), opts: opts}
}

func (r *Runner) Run(ctx context.Context, userInput string) (StepResult, error) {
	startedAt := time.Now()
	r.todos = todo.New()

	readPrompt := fmt.Sprintf("你是read阶段。提炼用户请求要点，不执行任何操作。\n用户请求：%s", userInput)
	readSummary, err := r.llm.Ask(ctx, readPrompt)
	if err != nil {
		return StepResult{}, err
	}

	planPrompt := fmt.Sprintf(`你是plan阶段。基于read摘要输出严格JSON，不要输出其它文字。
JSON Schema:
{
  "goal": "string",
  "steps": [
    {
      "id": "s1",
      "title": "string",
      "reason": "string",
      "risk": "low|medium|high",
      "requires_approval": true|false
    }
  ]
}
要求：steps 3-7条，按执行顺序。
read摘要：%s`, readSummary)
	planRaw, err := r.llm.Ask(ctx, planPrompt)
	if err != nil {
		return StepResult{}, err
	}
	plan := parsePlan(planRaw)
	if fixed, ok := normalizePlan(plan); ok {
		plan = fixed
	} else {
		repairPrompt := fmt.Sprintf(`你是plan修复阶段。将下面内容修复为严格JSON，不要输出其它文字。
Schema:
{"goal":"string","steps":[{"id":"s1","title":"string","reason":"string","risk":"low|medium|high","requires_approval":true}]}
原始内容：
%s`, planRaw)
		repairedRaw, rerr := r.llm.Ask(ctx, repairPrompt)
		if rerr == nil {
			repaired := parsePlan(repairedRaw)
			if fixed2, ok2 := normalizePlan(repaired); ok2 {
				plan = fixed2
			}
		}
	}
	if _, ok := normalizePlan(plan); !ok {
		return StepResult{}, fmt.Errorf("invalid plan: unable to normalize plan output")
	}

	for _, step := range plan.Steps {
		r.todos.Upsert(step.Title, todo.StatusTodo)
	}

	actionLogs := make([]ActionStepLog, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		if strings.TrimSpace(step.ID) == "" {
			step.ID = fmt.Sprintf("s%d", i+1)
		}
		if strings.TrimSpace(step.Title) == "" {
			continue
		}

		if (strings.EqualFold(step.Risk, "high") || step.RequiresApproval) && r.opts.Approver != nil {
			approved, aerr := r.opts.Approver(ctx, step)
			if aerr != nil {
				return StepResult{}, aerr
			}
			if !approved {
				r.todos.Upsert(step.Title, todo.StatusSkipped)
				actionLogs = append(actionLogs, ActionStepLog{StepID: step.ID, Title: step.Title, Status: string(todo.StatusSkipped), Attempts: 0})
				continue
			}
		}

		r.todos.Upsert(step.Title, todo.StatusInProgress)
		var success bool
		var lastErr error
		var out string
		attempts := 0

		for attempt := 1; attempt <= r.opts.MaxActRetries; attempt++ {
			attempts = attempt
			actPrompt := fmt.Sprintf("你是act阶段。只执行当前一步并简洁汇报结果。\n当前步骤：%s\n步骤原因：%s\n步骤风险：%s\n完整todo：\n%s", step.Title, step.Reason, step.Risk, r.todos.Render())
			resp, askErr := r.llm.Ask(ctx, actPrompt)
			if askErr != nil {
				lastErr = askErr
				continue
			}
			out = strings.TrimSpace(resp)
			success = true
			break
		}

		if success {
			r.todos.Upsert(step.Title, todo.StatusDone)
			actionLogs = append(actionLogs, ActionStepLog{StepID: step.ID, Title: step.Title, Status: string(todo.StatusDone), Attempts: attempts, Output: out})
			continue
		}

		r.todos.Upsert(step.Title, todo.StatusBlocked)
		errText := "act failed"
		if lastErr != nil {
			errText = lastErr.Error()
		}
		actionLogs = append(actionLogs, ActionStepLog{StepID: step.ID, Title: step.Title, Status: string(todo.StatusBlocked), Attempts: attempts, ErrorText: errText})
	}

	actionText := renderActionLogs(actionLogs)
	finalPrompt := fmt.Sprintf("基于以下执行记录，输出最终答复（先结论后细节，中文，简洁）。\n\n计划目标：%s\n\n%s", plan.Goal, actionText)
	final, err := r.llm.Ask(ctx, finalPrompt)
	if err != nil {
		return StepResult{}, err
	}

	auditPath, auditErr := r.saveRunAudit(startedAt, userInput, strings.TrimSpace(readSummary), plan, actionLogs, strings.TrimSpace(final))
	if auditErr != nil {
		auditPath = ""
	}

	return StepResult{
		ReadSummary: strings.TrimSpace(readSummary),
		Plan:        plan,
		ActionLogs:  actionLogs,
		Final:       strings.TrimSpace(final),
		AuditPath:   auditPath,
	}, nil
}

func (r *Runner) TodosText() string {
	return r.todos.Render()
}

func parsePlan(raw string) Plan {
	text := strings.TrimSpace(raw)
	text = stripCodeFence(text)

	var p Plan
	if err := json.Unmarshal([]byte(text), &p); err == nil && len(p.Steps) > 0 {
		return p
	}

	bullets := parseBullets(raw)
	steps := make([]PlanStep, 0, len(bullets))
	for i, b := range bullets {
		steps = append(steps, PlanStep{ID: fmt.Sprintf("s%d", i+1), Title: b, Reason: "fallback from bullets", Risk: "medium", RequiresApproval: false})
	}
	return Plan{Goal: "完成用户请求", Steps: steps}
}

func normalizePlan(p Plan) (Plan, bool) {
	p.Goal = strings.TrimSpace(p.Goal)
	if p.Goal == "" {
		p.Goal = "完成用户请求"
	}

	if len(p.Steps) == 0 {
		return Plan{}, false
	}

	seen := make(map[string]struct{}, len(p.Steps))
	normalized := make([]PlanStep, 0, len(p.Steps))
	for i, s := range p.Steps {
		s.ID = strings.TrimSpace(s.ID)
		s.Title = strings.TrimSpace(s.Title)
		s.Reason = strings.TrimSpace(s.Reason)
		s.Risk = strings.ToLower(strings.TrimSpace(s.Risk))

		if s.Title == "" {
			continue
		}
		if s.ID == "" {
			s.ID = fmt.Sprintf("s%d", i+1)
		}
		if _, ok := seen[s.ID]; ok {
			s.ID = fmt.Sprintf("%s_%d", s.ID, i+1)
		}
		seen[s.ID] = struct{}{}

		switch s.Risk {
		case "low", "medium", "high":
		default:
			s.Risk = "medium"
		}
		if s.Reason == "" {
			s.Reason = "根据规划执行"
		}
		normalized = append(normalized, s)
	}

	if len(normalized) == 0 {
		return Plan{}, false
	}
	p.Steps = normalized
	return p, true
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 3 {
		return s
	}
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			end = i
			break
		}
	}
	if end <= 1 {
		return s
	}
	return strings.TrimSpace(strings.Join(lines[1:end], "\n"))
}

func renderActionLogs(logs []ActionStepLog) string {
	if len(logs) == 0 {
		return "(no action logs)"
	}
	var b strings.Builder
	for _, l := range logs {
		_, _ = fmt.Fprintf(&b, "- [%s] %s (attempts=%d)", l.Status, l.Title, l.Attempts)
		if strings.TrimSpace(l.Output) != "" {
			_, _ = fmt.Fprintf(&b, "\n  output: %s", strings.TrimSpace(l.Output))
		}
		if strings.TrimSpace(l.ErrorText) != "" {
			_, _ = fmt.Fprintf(&b, "\n  error: %s", strings.TrimSpace(l.ErrorText))
		}
		_, _ = fmt.Fprint(&b, "\n")
	}
	return strings.TrimSpace(b.String())
}

type runAudit struct {
	StartedAt   string          `json:"started_at"`
	UserInput   string          `json:"user_input"`
	ReadSummary string          `json:"read_summary"`
	Plan        Plan            `json:"plan"`
	ActionLogs  []ActionStepLog `json:"action_logs"`
	Final       string          `json:"final"`
	Todos       string          `json:"todos"`
}

func (r *Runner) saveRunAudit(startedAt time.Time, userInput, readSummary string, plan Plan, logs []ActionStepLog, final string) (string, error) {
	base := strings.TrimSpace(r.opts.AuditDir)
	if base == "" {
		base = filepath.Join(".gopi-pro", "runs")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}

	fileName := fmt.Sprintf("run-%s.json", startedAt.Format("20060102-150405"))
	path := filepath.Join(base, fileName)
	payload := runAudit{
		StartedAt:   startedAt.Format(time.RFC3339),
		UserInput:   userInput,
		ReadSummary: readSummary,
		Plan:        plan,
		ActionLogs:  logs,
		Final:       final,
		Todos:       r.TodosText(),
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func parseBullets(s string) []string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	re := regexp.MustCompile(`^\s*[-*\d\.]+\s*(.+)$`)
	out := make([]string, 0)
	for _, line := range lines {
		m := re.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) == 2 {
			v := strings.TrimSpace(m[1])
			if v != "" {
				out = append(out, v)
			}
		}
	}
	if len(out) == 0 && strings.TrimSpace(s) != "" {
		out = append(out, strings.TrimSpace(s))
	}
	return out
}
