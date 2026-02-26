package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coderyrh/gopi-pro/internal/agent"
	"github.com/coderyrh/gopi-pro/internal/gopi"
)

func main() {
	var (
		gopiBin     = flag.String("gopi-bin", "../gopi/build/gopi.exe", "path to gopi binary")
		workdir     = flag.String("cwd", "", "working directory for task")
		timeout     = flag.Int("timeout", 120, "timeout seconds for each LLM call")
		autoApprove = flag.Bool("auto-approve", false, "auto approve high-risk steps")
		maxRetries  = flag.Int("max-retries", 2, "max retries for each action step")
		auditDir    = flag.String("audit-dir", ".gopi-pro/runs", "directory to persist run audit json")
	)
	flag.Parse()

	cwd := strings.TrimSpace(*workdir)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	client := gopi.New(*gopiBin, cwd)
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
