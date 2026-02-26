package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coderyrh/gopi-pro/internal/agent"
	"github.com/coderyrh/gopi-pro/internal/gopi"
)

func main() {
	var (
		gopiBin       = flag.String("gopi-bin", "../gopi/build/gopi.exe", "path to gopi binary")
		workdir       = flag.String("cwd", "", "working directory for task")
		timeout       = flag.Int("timeout", 120, "timeout seconds for each LLM call")
		autoApprove   = flag.Bool("auto-approve", false, "auto approve high-risk steps")
		maxRetries    = flag.Int("max-retries", 2, "max retries for each action step")
		auditDir      = flag.String("audit-dir", ".gopi-pro/runs", "directory to persist run audit json")
		showAudit     = flag.Bool("show-audit", false, "show latest audit summary and exit")
		showAuditFull = flag.Bool("show-audit-full", false, "show selected audit raw json and exit")
		auditIndex    = flag.Int("show-audit-index", 1, "which latest audit to show, 1 means most recent")
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
	runner := agent.NewRunner(
		timeoutLLM{inner: client, timeout: time.Duration(*timeout) * time.Second},
		agent.RunnerOptions{
			MaxActRetries: *maxRetries,
			AuditDir:      *auditDir,
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

		res, err := runner.Run(context.Background(), text)
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

type timeoutLLM struct {
	inner interface {
		Ask(ctx context.Context, prompt string) (string, error)
	}
	timeout time.Duration
}

func (t timeoutLLM) Ask(_ context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	return t.inner.Ask(ctx, prompt)
}
