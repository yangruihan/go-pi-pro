package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yangruihan/go-pi-pro/internal/agent"
	"github.com/yangruihan/go-pi-pro/internal/gopi"
)

func main() {
	var (
		gopiBin       = flag.String("gopi-bin", "../gopi/build/gopi.exe", "path to gopi binary")
		workdir       = flag.String("cwd", "", "working directory for task")
		timeout       = flag.Int("timeout", 300, "timeout seconds for each LLM call")
		autoApprove   = flag.Bool("auto-approve", false, "auto approve high-risk steps")
		maxRetries    = flag.Int("max-retries", 2, "max retries for each action step")
		auditDir      = flag.String("audit-dir", ".gopi-pro/runs", "directory to persist run audit json")
		showAudit     = flag.Bool("show-audit", false, "show latest audit summary and exit")
		showAuditFull = flag.Bool("show-audit-full", false, "show selected audit raw json and exit")
		auditIndex    = flag.Int("show-audit-index", 1, "which latest audit to show, 1 means most recent")
		noSpinner     = flag.Bool("no-spinner", false, "disable thinking spinner output")
	)
	flag.Parse()

	cwd := strings.TrimSpace(*workdir)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	if *showAudit || *showAuditFull {
		if err := printAudit(*auditDir, *auditIndex, *showAuditFull); err != nil {
			fmt.Fprintf(os.Stderr, "show audit failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	client := gopi.New(*gopiBin, cwd)
	defer client.Close()
	printRuntimeInfo(client.Info())
	runner := agent.NewRunner(
		timeoutLLM{inner: client, timeout: time.Duration(*timeout) * time.Second},
		agent.RunnerOptions{
			MaxActRetries: *maxRetries,
			AuditDir:      *auditDir,
			WorkingDir:    cwd,
			OnProgress: func(ev agent.ProgressEvent) {
				phase := strings.ToUpper(strings.TrimSpace(ev.Phase))
				if phase == "" {
					phase = "PROGRESS"
				}
				if ev.Total > 0 {
					fmt.Printf("\n[%s] %s (%d/%d)\n", phase, strings.TrimSpace(ev.Message), ev.Completed, ev.Total)
				} else {
					fmt.Printf("\n[%s] %s\n", phase, strings.TrimSpace(ev.Message))
				}
				if strings.TrimSpace(ev.TodoText) != "" && ev.TodoText != "(no todos)" {
					fmt.Println(ev.TodoText)
				}
			},
			Approver: func(_ context.Context, step agent.PlanStep) (bool, error) {
				if *autoApprove {
					return true, nil
				}
				fmt.Printf("\n[APPROVAL] step=%s risk=%s\n%s\n批准执行? (y/N): ", step.ID, step.Risk, step.Title)
				reader := bufio.NewReader(os.Stdin)
				line, err := reader.ReadString('\n')
				if err != nil {
					return false, err
				}
				v := strings.ToLower(strings.TrimSpace(line))
				return v == "y" || v == "yes", nil
			},
		},
	)

	fmt.Println("gopi-pro (read-plan-act) ready. 输入你的任务，Ctrl+C 退出。")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			fmt.Println("\nbye")
			return
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}

		var indicator *thinkingIndicator
		if !*noSpinner {
			indicator = newThinkingIndicator()
		}
		res, err := runner.Run(context.Background(), text)
		if indicator != nil {
			indicator.StopAndClear()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		fmt.Println("\n[READ]")
		fmt.Println(res.ReadSummary)
		fmt.Println("\n[PLAN]")
		fmt.Printf("Goal: %s\n", res.Plan.Goal)
		for i, step := range res.Plan.Steps {
			fmt.Printf("%d. (%s) %s [risk=%s approval=%v]\n", i+1, step.ID, step.Title, step.Risk, step.RequiresApproval)
		}
		fmt.Println("\n[TODOS]")
		fmt.Println(runner.TodosText())
		fmt.Println("\n[ACTION]")
		for _, log := range res.ActionLogs {
			fmt.Printf("- [%s] %s (attempts=%d)\n", log.Status, log.Title, log.Attempts)
			if strings.TrimSpace(log.Output) != "" {
				fmt.Printf("  output: %s\n", log.Output)
			}
			if strings.TrimSpace(log.ErrorText) != "" {
				fmt.Printf("  error: %s\n", log.ErrorText)
			}
		}
		fmt.Println("\n[FINAL]")
		fmt.Println(res.Final)
		if strings.TrimSpace(res.AuditPath) != "" {
			fmt.Printf("\n[AUDIT]\n%s\n", res.AuditPath)
		}
	}
}

type thinkingIndicator struct {
	stopCh  chan struct{}
	doneCh  chan struct{}
	stopped atomic.Bool
}

func newThinkingIndicator() *thinkingIndicator {
	ti := &thinkingIndicator{
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go func() {
		defer close(ti.doneCh)
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ti.stopCh:
				return
			case <-ticker.C:
				fmt.Printf("\r[%s] 思考中...", frames[i%len(frames)])
				i++
			}
		}
	}()
	return ti
}

func (ti *thinkingIndicator) StopAndClear() {
	if ti == nil {
		return
	}
	if !ti.stopped.CompareAndSwap(false, true) {
		return
	}
	close(ti.stopCh)
	<-ti.doneCh
	fmt.Print("\r                \r")
}

type auditView struct {
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
	DurationMs  int64  `json:"duration_ms"`
	UserInput   string `json:"user_input"`
	ReadSummary string `json:"read_summary"`
	Plan        struct {
		Goal  string `json:"goal"`
		Steps []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Risk  string `json:"risk"`
		} `json:"steps"`
	} `json:"plan"`
	ActionLogs []struct {
		StepID   string `json:"step_id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Attempts int    `json:"attempts"`
	} `json:"action_logs"`
	Final string `json:"final"`
}

func printAudit(auditDir string, index int, full bool) error {
	base := strings.TrimSpace(auditDir)
	if base == "" {
		base = filepath.Join(".gopi-pro", "runs")
	}
	if index <= 0 {
		index = 1
	}

	files, err := listAuditFiles(base)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("[LATEST AUDIT]\n(no audit directory) %s\n", base)
			return nil
		}
		return err
	}
	if len(files) == 0 {
		fmt.Printf("[LATEST AUDIT]\n(no audit files) %s\n", base)
		return nil
	}
	if index > len(files) {
		return fmt.Errorf("show-audit-index=%d out of range, available=%d", index, len(files))
	}
	target := files[index-1]

	b, err := os.ReadFile(target.path)
	if err != nil {
		return err
	}

	if full {
		fmt.Println("[LATEST AUDIT FULL]")
		fmt.Println(target.path)
		fmt.Println(string(b))
		return nil
	}

	var audit auditView
	if err := json.Unmarshal(b, &audit); err != nil {
		return err
	}

	final := strings.TrimSpace(audit.Final)
	if final == "" {
		final = "(empty)"
	}
	if len(final) > 240 {
		final = final[:240] + "..."
	}

	var doneCount, blockedCount, skippedCount int
	for _, log := range audit.ActionLogs {
		switch strings.ToLower(strings.TrimSpace(log.Status)) {
		case "done":
			doneCount++
		case "blocked":
			blockedCount++
		case "skipped":
			skippedCount++
		}
	}

	fmt.Println("[LATEST AUDIT]")
	fmt.Println(target.path)
	fmt.Printf("started_at: %s\n", strings.TrimSpace(audit.StartedAt))
	fmt.Printf("finished_at: %s\n", strings.TrimSpace(audit.FinishedAt))
	fmt.Printf("duration_ms: %d\n", audit.DurationMs)
	fmt.Printf("goal: %s\n", strings.TrimSpace(audit.Plan.Goal))
	fmt.Printf("steps: %d\n", len(audit.Plan.Steps))
	fmt.Printf("action_logs: %d (done=%d blocked=%d skipped=%d)\n", len(audit.ActionLogs), doneCount, blockedCount, skippedCount)
	fmt.Printf("user_input: %s\n", strings.TrimSpace(audit.UserInput))
	fmt.Printf("final: %s\n", final)
	return nil
}

type auditFile struct {
	name    string
	path    string
	modTime time.Time
}

func listAuditFiles(base string) ([]auditFile, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	files := make([]auditFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "run-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		fullPath := filepath.Join(base, entry.Name())
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		files = append(files, auditFile{name: entry.Name(), path: fullPath, modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name > files[j].name
		}
		return files[i].modTime.After(files[j].modTime)
	})
	return files, nil
}

func printRuntimeInfo(info gopi.RuntimeInfo) {
	fmt.Println("[RUNTIME]")
	fmt.Printf("mode: %s\n", strings.TrimSpace(info.Mode))
	fmt.Printf("provider: %s\n", strings.TrimSpace(info.Provider))
	fmt.Printf("model: %s\n", strings.TrimSpace(info.Model))
	if strings.TrimSpace(info.ConfigModel) != "" {
		fmt.Printf("configured_model: %s\n", strings.TrimSpace(info.ConfigModel))
	}
	if strings.TrimSpace(info.SessionModel) != "" {
		fmt.Printf("session_model: %s\n", strings.TrimSpace(info.SessionModel))
	}
	fmt.Printf("host: %s\n", strings.TrimSpace(info.Host))
	fmt.Printf("api_base: %s\n", strings.TrimSpace(info.APIBase))
	fmt.Printf("session_id: %s\n", strings.TrimSpace(info.SessionID))
	if len(info.ConfigPaths) > 0 {
		fmt.Printf("config_paths: %s\n", strings.Join(info.ConfigPaths, " -> "))
	} else {
		fmt.Println("config_paths: (default built-in / managed by gopi binary)")
	}
	fmt.Printf("cwd: %s\n\n", strings.TrimSpace(info.CWD))
}

type timeoutLLM struct {
	inner interface {
		Ask(ctx context.Context, prompt string) (string, error)
	}
	timeout time.Duration
}

type askWithStatsInner interface {
	AskWithStats(ctx context.Context, prompt string) (text string, toolCalls int, writeToolCalls int, err error)
}

func (t timeoutLLM) Ask(parentCtx context.Context, prompt string) (string, error) {
	out, err := t.askWithStreamingRetry(parentCtx, prompt, t.timeout)
	if err == nil {
		return out, nil
	}
	if !isTimeoutErr(err) {
		return "", err
	}

	retryTimeout := t.timeout * 2
	if retryTimeout < 60*time.Second {
		retryTimeout = 60 * time.Second
	}
	return t.askWithStreamingRetry(parentCtx, prompt, retryTimeout)
}

func (t timeoutLLM) AskWithStats(parentCtx context.Context, prompt string) (string, int, int, error) {
	out, toolCalls, writeToolCalls, err := t.askWithStatsRetry(parentCtx, prompt, t.timeout)
	if err == nil {
		return out, toolCalls, writeToolCalls, nil
	}
	if !isTimeoutErr(err) {
		return "", 0, 0, err
	}

	retryTimeout := t.timeout * 2
	if retryTimeout < 60*time.Second {
		retryTimeout = 60 * time.Second
	}
	return t.askWithStatsRetry(parentCtx, prompt, retryTimeout)
}

func (t timeoutLLM) askWithStreamingRetry(parentCtx context.Context, prompt string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	var lastErr error
	for i := 0; i < 8; i++ {
		out, err := t.inner.Ask(ctx, prompt)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isAlreadyStreamingErr(err) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", lastErr
}

func (t timeoutLLM) askWithStatsRetry(parentCtx context.Context, prompt string, timeout time.Duration) (string, int, int, error) {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	inner, ok := t.inner.(askWithStatsInner)
	if !ok {
		out, err := t.inner.Ask(ctx, prompt)
		return out, -1, -1, err
	}

	var lastErr error
	for i := 0; i < 8; i++ {
		out, toolCalls, writeToolCalls, err := inner.AskWithStats(ctx, prompt)
		if err == nil {
			return out, toolCalls, writeToolCalls, nil
		}
		lastErr = err
		if !isAlreadyStreamingErr(err) {
			return "", 0, 0, err
		}
		select {
		case <-ctx.Done():
			return "", 0, 0, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", 0, 0, lastErr
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timeout")
}

func isAlreadyStreamingErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "agent is already streaming")
}
