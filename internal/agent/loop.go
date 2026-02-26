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

	"github.com/yangruihan/go-pi-pro/internal/todo"
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
	r.emitProgress("read", "分析用户请求", 0, 0)

	readPrompt := buildReadPrompt(userInput)
	readSummary, err := r.llm.Ask(ctx, readPrompt)
	if err != nil {
		return StepResult{}, err
	}
	r.emitProgress("read", "完成需求提炼", 0, 0)

	r.emitProgress("plan", "生成执行计划", 0, 0)
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
	r.emitProgress("plan", "计划生成完成", 0, 0)

	for _, step := range plan.Steps {
		r.todos.Upsert(step.Title, todo.StatusTodo)
	}
	r.emitProgress("todo", "初始化待办项", len(plan.Steps), countCompleted(r.todos.All()))

	requestedFiles := detectRequestedFiles(userInput)

	actionLogs := make([]ActionStepLog, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		if strings.TrimSpace(step.ID) == "" {
			step.ID = fmt.Sprintf("s%d", i+1)
		}
		if strings.TrimSpace(step.Title) == "" {
			continue
		}

		if isIntentConfirmationStep(step) {
			r.todos.Upsert(step.Title, todo.StatusDone)
			r.emitProgress("act", fmt.Sprintf("步骤完成: %s", step.Title), len(plan.Steps), countCompleted(r.todos.All()))
			actionLogs = append(actionLogs, ActionStepLog{
				StepID:         step.ID,
				Title:          step.Title,
				Status:         string(todo.StatusDone),
				Attempts:       1,
				Output:         fmt.Sprintf("已基于read阶段完成用户意图确认：%s", strings.TrimSpace(readSummary)),
				ToolCalls:      0,
				WriteToolCalls: 0,
			})
			continue
		}

		if isLocalProbeStep(step) {
			r.todos.Upsert(step.Title, todo.StatusDone)
			r.emitProgress("act", fmt.Sprintf("步骤完成: %s", step.Title), len(plan.Steps), countCompleted(r.todos.All()))
			actionLogs = append(actionLogs, ActionStepLog{
				StepID:         step.ID,
				Title:          step.Title,
				Status:         string(todo.StatusDone),
				Attempts:       1,
				Output:         r.buildLocalProbeOutput(step, requestedFiles),
				ToolCalls:      0,
				WriteToolCalls: 0,
			})
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
		r.emitProgress("act", fmt.Sprintf("执行步骤: %s", step.Title), len(plan.Steps), countCompleted(r.todos.All()))
		var success bool
		var lastErr error
		var out string
		lastToolCalls := -1
		lastWriteToolCalls := -1
		attempts := 0
		stepWriteIntent := isStrictWriteFileStep(step, requestedFiles)
		stepExpectedFiles := expectedFilesForStep(step, requestedFiles)

		for attempt := 1; attempt <= r.opts.MaxActRetries; attempt++ {
			attempts = attempt
			actPrompt := fmt.Sprintf("你是act阶段。只执行当前一步并简洁汇报结果。\n当前步骤：%s\n步骤原因：%s\n步骤风险：%s\n完整todo：\n%s", step.Title, step.Reason, step.Risk, r.todos.Render())
			if stepWriteIntent {
				if len(stepExpectedFiles) > 0 {
					actPrompt = fmt.Sprintf("%s\n\n强约束：这是写文件步骤，必须通过真实工具调用完成文件写入，严禁仅口头描述完成。目标文件：%s。若无法写入请明确失败原因。", actPrompt, strings.Join(stepExpectedFiles, ", "))
				} else {
					actPrompt = fmt.Sprintf("%s\n\n强约束：这是写文件步骤，必须通过真实工具调用完成文件写入，严禁仅口头描述完成。若无法写入请明确失败原因。", actPrompt)
				}
				if attempt > 1 && lastErr != nil {
					actPrompt = fmt.Sprintf("%s\n上次失败原因：%s\n本次必须先完成 write_file 工具调用，再输出结果。", actPrompt, strings.TrimSpace(lastErr.Error()))
				}
			}
			resp, toolCalls, writeToolCalls, askErr := askWithStats(ctx, r.llm, actPrompt)
			if askErr != nil {
				lastErr = askErr
				continue
			}
			lastToolCalls = toolCalls
			lastWriteToolCalls = writeToolCalls
			out = strings.TrimSpace(resp)
			if stepWriteIntent && len(stepExpectedFiles) > 0 {
				missing := findMissingFiles(stepExpectedFiles, r.resolveWorkingDir())
				if len(missing) > 0 {
					lastErr = fmt.Errorf("%s", buildWriteFailureReason(missing, toolCalls, writeToolCalls))
					continue
				}
			}
			success = true
			break
		}

		if success {
			r.todos.Upsert(step.Title, todo.StatusDone)
			r.emitProgress("act", fmt.Sprintf("步骤完成: %s", step.Title), len(plan.Steps), countCompleted(r.todos.All()))
			actionLogs = append(actionLogs, ActionStepLog{StepID: step.ID, Title: step.Title, Status: string(todo.StatusDone), Attempts: attempts, Output: out, ToolCalls: normalizeStat(lastToolCalls), WriteToolCalls: normalizeStat(lastWriteToolCalls)})
			continue
		}

		r.todos.Upsert(step.Title, todo.StatusBlocked)
		r.emitProgress("act", fmt.Sprintf("步骤阻塞: %s", step.Title), len(plan.Steps), countCompleted(r.todos.All()))
		errText := "act failed"
		if lastErr != nil {
			errText = lastErr.Error()
		}
		actionLogs = append(actionLogs, ActionStepLog{StepID: step.ID, Title: step.Title, Status: string(todo.StatusBlocked), Attempts: attempts, ErrorText: errText, ToolCalls: normalizeStat(lastToolCalls), WriteToolCalls: normalizeStat(lastWriteToolCalls)})

		for j := i + 1; j < len(plan.Steps); j++ {
			next := plan.Steps[j]
			if strings.TrimSpace(next.ID) == "" {
				next.ID = fmt.Sprintf("s%d", j+1)
			}
			if strings.TrimSpace(next.Title) == "" {
				continue
			}
			r.todos.Upsert(next.Title, todo.StatusSkipped)
			actionLogs = append(actionLogs, ActionStepLog{
				StepID:    next.ID,
				Title:     next.Title,
				Status:    string(todo.StatusSkipped),
				Attempts:  0,
				ErrorText: fmt.Sprintf("前置步骤失败，已跳过：%s", step.Title),
			})
		}
		break
	}

	actionText := renderActionLogs(actionLogs)
	r.emitProgress("final", "生成最终答复", len(plan.Steps), countCompleted(r.todos.All()))
	final := ""
	if blocked, ok := firstBlockedAction(actionLogs); ok {
		final = buildBlockedFinal(plan.Goal, blocked)
	} else {
		finalPrompt := fmt.Sprintf("基于以下执行记录，输出最终答复（先结论后细节，中文，简洁）。\n\n计划目标：%s\n\n%s", plan.Goal, actionText)
		generated, ferr := r.llm.Ask(ctx, finalPrompt)
		if ferr != nil {
			return StepResult{}, ferr
		}
		final = generated
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

func (r *Runner) emitProgress(phase, message string, total, completed int) {
	if r.opts.OnProgress == nil {
		return
	}
	r.opts.OnProgress(ProgressEvent{
		Phase:     phase,
		Message:   message,
		Total:     total,
		Completed: completed,
		TodoText:  r.TodosText(),
	})
}

func (r *Runner) TodosText() string {
	return r.todos.Render()
}

func buildReadPrompt(userInput string) string {
	return fmt.Sprintf(`你是read阶段。提炼用户请求要点，不执行任何操作。
你可以访问当前会话历史。
如果用户在问“我刚才问过什么/之前问过什么/历史问题”，必须先基于会话历史中的用户消息进行归纳回答；
只有当历史里确实没有可用的更早用户消息时，才可以说明无法回忆。
输出要求：只输出提炼后的结论，不要输出工具调用或多余前后缀。
用户请求：%s`, userInput)
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

func (r *Runner) resolveWorkingDir() string {
	if strings.TrimSpace(r.opts.WorkingDir) != "" {
		return r.opts.WorkingDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

func hasWriteIntent(s string) bool {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return false
	}
	keys := []string{"写", "创建", "保存", "生成文件", "write", "create", "save", "file"}
	for _, k := range keys {
		if strings.Contains(v, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func detectRequestedFiles(userInput string) []string {
	text := strings.ReplaceAll(userInput, "\\", "/")
	re := regexp.MustCompile(`([A-Za-z0-9._/-]+\.[A-Za-z0-9]{1,16})`)
	matches := re.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		candidate := strings.TrimSpace(m)
		candidate = strings.Trim(candidate, "`\"'.,;:()[]{}")
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, "://") {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func findMissingFiles(files []string, workingDir string) []string {
	missing := make([]string, 0)
	for _, f := range files {
		p := strings.TrimSpace(f)
		if p == "" {
			continue
		}
		full := p
		if !filepath.IsAbs(full) {
			full = filepath.Join(workingDir, full)
		}
		if _, err := os.Stat(full); err != nil {
			missing = append(missing, p)
		}
	}
	return missing
}

func expectedFilesForStep(step PlanStep, requestedFiles []string) []string {
	stepText := strings.TrimSpace(step.Title + " " + step.Reason)
	if stepText == "" {
		return requestedFiles
	}
	stepFiles := detectRequestedFiles(stepText)
	if len(stepFiles) == 0 {
		if isTextStrictFileWriteIntent(stepText) {
			return requestedFiles
		}
		return nil
	}

	if len(requestedFiles) == 0 {
		return stepFiles
	}

	reqSet := make(map[string]struct{}, len(requestedFiles))
	for _, f := range requestedFiles {
		reqSet[filepath.ToSlash(strings.TrimSpace(f))] = struct{}{}
	}

	out := make([]string, 0, len(stepFiles))
	for _, f := range stepFiles {
		key := filepath.ToSlash(strings.TrimSpace(f))
		if _, ok := reqSet[key]; ok {
			out = append(out, key)
		}
	}
	if len(out) == 0 && isTextStrictFileWriteIntent(stepText) {
		return requestedFiles
	}
	return out
}

func isStrictWriteFileStep(step PlanStep, requestedFiles []string) bool {
	stepText := strings.TrimSpace(step.Title + " " + step.Reason)
	if stepText == "" {
		return false
	}
	if len(detectRequestedFiles(stepText)) > 0 {
		return true
	}
	if len(requestedFiles) > 0 && isTextStrictFileWriteIntent(stepText) {
		return true
	}
	return false
}

func isTextStrictFileWriteIntent(text string) bool {
	v := strings.ToLower(strings.TrimSpace(text))
	if v == "" {
		return false
	}
	keys := []string{
		"写入文件", "写到文件", "保存到", "落盘", "输出到文件",
		"write_file", "write to file", "save to", "persist to file",
	}
	for _, k := range keys {
		if strings.Contains(v, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func countCompleted(items []todo.Item) int {
	count := 0
	for _, it := range items {
		if it.Status == todo.StatusDone || it.Status == todo.StatusSkipped {
			count++
		}
	}
	return count
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
		if l.ToolCalls > 0 || l.WriteToolCalls > 0 {
			_, _ = fmt.Fprintf(&b, "\n  tool_calls: %d (write_file=%d)", l.ToolCalls, l.WriteToolCalls)
		}
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

func askWithStats(ctx context.Context, llmClient LLM, prompt string) (string, int, int, error) {
	if withStats, ok := llmClient.(LLMWithStats); ok {
		text, toolCalls, writeToolCalls, err := withStats.AskWithStats(ctx, prompt)
		return text, toolCalls, writeToolCalls, err
	}
	text, err := llmClient.Ask(ctx, prompt)
	return text, -1, -1, err
}

func buildWriteFailureReason(missing []string, toolCalls, writeToolCalls int) string {
	base := fmt.Sprintf("步骤未真实落盘，缺失文件: %s", strings.Join(missing, ", "))
	if toolCalls == 0 {
		return base + "；原因：未观测到任何工具调用"
	}
	if toolCalls > 0 && writeToolCalls == 0 {
		return base + fmt.Sprintf("；原因：观测到工具调用=%d，但未观测到 write_file 调用", toolCalls)
	}
	if writeToolCalls > 0 {
		return base + fmt.Sprintf("；原因：已观测到 write_file 调用=%d，但目标文件仍不存在（可能路径不一致或写入失败）", writeToolCalls)
	}
	return base + "；原因：工具调用信息不可用"
}

func normalizeStat(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func (r *Runner) buildLocalProbeOutput(step PlanStep, requestedFiles []string) string {
	wd := r.resolveWorkingDir()
	stepText := strings.TrimSpace(step.Title + " " + step.Reason)
	files := detectRequestedFiles(stepText)
	if len(files) == 0 {
		files = requestedFiles
	}
	if len(files) == 0 {
		return fmt.Sprintf("当前工作目录为 %s，本地检查完成。", wd)
	}

	missing := findMissingFiles(files, wd)
	if len(missing) == 0 {
		return fmt.Sprintf("当前工作目录为 %s，目标文件已存在：%s。", wd, strings.Join(files, ", "))
	}

	existing := make([]string, 0, len(files))
	missingSet := make(map[string]struct{}, len(missing))
	for _, m := range missing {
		missingSet[filepath.ToSlash(strings.TrimSpace(m))] = struct{}{}
	}
	for _, f := range files {
		k := filepath.ToSlash(strings.TrimSpace(f))
		if _, ok := missingSet[k]; !ok {
			existing = append(existing, f)
		}
	}

	if len(existing) == 0 {
		return fmt.Sprintf("当前工作目录为 %s，目标文件均不存在：%s。", wd, strings.Join(missing, ", "))
	}
	return fmt.Sprintf("当前工作目录为 %s，已存在文件：%s；不存在文件：%s。", wd, strings.Join(existing, ", "), strings.Join(missing, ", "))
}

func isIntentConfirmationStep(step PlanStep) bool {
	text := strings.ToLower(strings.TrimSpace(step.Title + " " + step.Reason))
	if text == "" {
		return false
	}
	keys := []string{
		"确认用户意图",
		"确认意图",
		"确认需求",
		"理解用户需求",
		"clarify intent",
		"confirm intent",
		"confirm requirement",
	}
	for _, key := range keys {
		if strings.Contains(text, strings.ToLower(key)) {
			return true
		}
	}
	return false
}

func isLocalProbeStep(step PlanStep) bool {
	text := strings.ToLower(strings.TrimSpace(step.Title + " " + step.Reason))
	if text == "" {
		return false
	}
	keys := []string{
		"确认工作目录",
		"检查当前目录",
		"文件存在性",
		"检查文件",
		"check current directory",
		"check file existence",
		"verify file exists",
	}
	for _, key := range keys {
		if strings.Contains(text, strings.ToLower(key)) {
			return true
		}
	}
	return false
}

type runAudit struct {
	StartedAt   string          `json:"started_at"`
	FinishedAt  string          `json:"finished_at"`
	DurationMs  int64           `json:"duration_ms"`
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
	finishedAt := time.Now()
	payload := runAudit{
		StartedAt:   startedAt.Format(time.RFC3339),
		FinishedAt:  finishedAt.Format(time.RFC3339),
		DurationMs:  finishedAt.Sub(startedAt).Milliseconds(),
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

func firstBlockedAction(logs []ActionStepLog) (ActionStepLog, bool) {
	for _, l := range logs {
		if strings.EqualFold(strings.TrimSpace(l.Status), string(todo.StatusBlocked)) {
			return l, true
		}
	}
	return ActionStepLog{}, false
}

func buildBlockedFinal(goal string, blocked ActionStepLog) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = "完成用户请求"
	}
	msg := strings.TrimSpace(blocked.ErrorText)
	if msg == "" {
		msg = "关键步骤执行失败"
	}
	return fmt.Sprintf("结论：任务未完成。\n\n计划目标：%s\n失败步骤：%s\n失败原因：%s\n建议：修复失败原因后重试。", goal, strings.TrimSpace(blocked.Title), msg)
}
