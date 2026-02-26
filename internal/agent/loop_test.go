package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangruihan/go-pi-pro/internal/todo"
)

func TestParsePlanJSON(t *testing.T) {
	raw := `{"goal":"完成任务","steps":[{"id":"s1","title":"读取文件","reason":"先了解上下文","risk":"low","requires_approval":false}]}`
	p := parsePlan(raw)
	if p.Goal != "完成任务" {
		t.Fatalf("unexpected goal: %s", p.Goal)
	}
	if len(p.Steps) != 1 || p.Steps[0].Title != "读取文件" {
		t.Fatalf("unexpected steps: %#v", p.Steps)
	}
}

func TestParsePlanFallbackBullets(t *testing.T) {
	raw := "- step a\n- step b\n3. step c"
	p := parsePlan(raw)
	if len(p.Steps) != 3 {
		t.Fatalf("expected 3, got %d", len(p.Steps))
	}
	if p.Steps[0].Title != "step a" || p.Steps[2].Title != "step c" {
		t.Fatalf("unexpected parse result: %#v", p.Steps)
	}
}

func TestRenderActionLogs(t *testing.T) {
	text := renderActionLogs([]ActionStepLog{{Title: "A", Status: "done", Attempts: 1, Output: "ok"}})
	if text == "" || text == "(no action logs)" {
		t.Fatalf("unexpected render result: %s", text)
	}
}

func TestNormalizePlan(t *testing.T) {
	p := Plan{
		Goal: "",
		Steps: []PlanStep{
			{ID: "", Title: "  step a  ", Risk: "HIGH", Reason: ""},
			{ID: "s1", Title: "step b", Risk: "unknown", Reason: "x"},
			{ID: "s1", Title: "step c", Risk: "low", Reason: "y"},
		},
	}
	n, ok := normalizePlan(p)
	if !ok {
		t.Fatalf("expected normalize success")
	}
	if n.Goal == "" {
		t.Fatalf("goal should be filled")
	}
	if len(n.Steps) != 3 {
		t.Fatalf("unexpected steps len: %d", len(n.Steps))
	}
	if n.Steps[0].ID == "" || n.Steps[1].ID == n.Steps[2].ID {
		t.Fatalf("ids not normalized: %#v", n.Steps)
	}
	if n.Steps[0].Risk != "high" || n.Steps[1].Risk != "medium" {
		t.Fatalf("risk normalize failed: %#v", n.Steps)
	}
}

func TestBuildReadPromptIncludesHistoryInstruction(t *testing.T) {
	p := buildReadPrompt("我刚才问过你哪些问题")
	if !strings.Contains(p, "你可以访问当前会话历史") {
		t.Fatalf("missing history instruction: %s", p)
	}
	if !strings.Contains(p, "只有当历史里确实没有可用的更早用户消息") {
		t.Fatalf("missing fallback constraint: %s", p)
	}
}

func TestDetectRequestedFiles(t *testing.T) {
	files := detectRequestedFiles("帮我写一个快速排序到 sort.py，并另存到 scripts/demo.py")
	if len(files) != 2 {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestFindMissingFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(existing, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	missing := findMissingFiles([]string{"a.txt", "b.txt"}, dir)
	if len(missing) != 1 || missing[0] != "b.txt" {
		t.Fatalf("unexpected missing: %#v", missing)
	}
}

func TestHasWriteIntent(t *testing.T) {
	if !hasWriteIntent("帮我写一个快排到 sort.py") {
		t.Fatalf("expected write intent")
	}
	if hasWriteIntent("你好吗") {
		t.Fatalf("unexpected write intent")
	}
}

func TestCountCompleted(t *testing.T) {
	items := []todo.Item{
		{ID: 1, Title: "a", Status: todo.StatusDone},
		{ID: 2, Title: "b", Status: todo.StatusSkipped},
		{ID: 3, Title: "c", Status: todo.StatusTodo},
	}
	if got := countCompleted(items); got != 2 {
		t.Fatalf("unexpected count: %d", got)
	}
}

func TestExpectedFilesForStep(t *testing.T) {
	requested := []string{"sort.py", "scripts/demo.py"}
	step := PlanStep{Title: "写入 sort.py", Reason: "生成快排"}
	files := expectedFilesForStep(step, requested)
	if len(files) != 1 || files[0] != "sort.py" {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestExpectedFilesForWriteStepFallbackToRequested(t *testing.T) {
	requested := []string{"sort.py"}
	step := PlanStep{Title: "创建代码文件", Reason: "写入实现"}
	files := expectedFilesForStep(step, requested)
	if len(files) != 0 {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestExpectedFilesForStrictWriteFallbackToRequested(t *testing.T) {
	requested := []string{"sort.py"}
	step := PlanStep{Title: "保存到文件", Reason: "落盘"}
	files := expectedFilesForStep(step, requested)
	if len(files) != 1 || files[0] != "sort.py" {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestBuildWriteFailureReason(t *testing.T) {
	missing := []string{"sort.py"}
	noTool := buildWriteFailureReason(missing, 0, 0)
	if !strings.Contains(noTool, "未观测到任何工具调用") {
		t.Fatalf("unexpected reason: %s", noTool)
	}

	noWrite := buildWriteFailureReason(missing, 2, 0)
	if !strings.Contains(noWrite, "未观测到 write_file 调用") {
		t.Fatalf("unexpected reason: %s", noWrite)
	}

	withWrite := buildWriteFailureReason(missing, 2, 1)
	if !strings.Contains(withWrite, "已观测到 write_file 调用") {
		t.Fatalf("unexpected reason: %s", withWrite)
	}
}

func TestNormalizeStat(t *testing.T) {
	if got := normalizeStat(-1); got != 0 {
		t.Fatalf("unexpected normalized stat: %d", got)
	}
	if got := normalizeStat(3); got != 3 {
		t.Fatalf("unexpected normalized stat: %d", got)
	}
}

func TestIsIntentConfirmationStep(t *testing.T) {
	if !isIntentConfirmationStep(PlanStep{Title: "确认用户意图", Reason: "避免误解任务"}) {
		t.Fatalf("expected true for intent confirmation")
	}
	if isIntentConfirmationStep(PlanStep{Title: "将代码写入 sort.py", Reason: "落盘"}) {
		t.Fatalf("expected false for write step")
	}
}

func TestIsLocalProbeStep(t *testing.T) {
	if !isLocalProbeStep(PlanStep{Title: "检查当前目录文件", Reason: "确认文件存在性"}) {
		t.Fatalf("expected true for local probe step")
	}
	if isLocalProbeStep(PlanStep{Title: "编写快速排序代码", Reason: "实现功能"}) {
		t.Fatalf("expected false for coding step")
	}
}

func TestBuildLocalProbeOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	r := NewRunner(nil, RunnerOptions{WorkingDir: dir})
	step := PlanStep{Title: "检查文件存在性 a.txt b.txt", Reason: "确认当前目录"}
	out := r.buildLocalProbeOutput(step, nil)
	if !strings.Contains(out, "已存在文件") || !strings.Contains(out, "不存在文件") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestIsStrictWriteFileStep(t *testing.T) {
	requested := []string{"sort.py"}
	if !isStrictWriteFileStep(PlanStep{Title: "将代码写入 sort.py 文件", Reason: "落盘"}, requested) {
		t.Fatalf("expected write file step")
	}
	if isStrictWriteFileStep(PlanStep{Title: "编写快速排序 Python 代码", Reason: "实现算法"}, requested) {
		t.Fatalf("coding step should not be treated as write file step")
	}
}

func TestFirstBlockedAction(t *testing.T) {
	logs := []ActionStepLog{
		{Title: "a", Status: string(todo.StatusDone)},
		{Title: "b", Status: string(todo.StatusBlocked), ErrorText: "x"},
	}
	b, ok := firstBlockedAction(logs)
	if !ok || b.Title != "b" {
		t.Fatalf("unexpected blocked action: %#v, ok=%v", b, ok)
	}
}

func TestBuildBlockedFinal(t *testing.T) {
	text := buildBlockedFinal("写入文件", ActionStepLog{Title: "将代码写入 sort.py 文件", ErrorText: "未观测到 write_file"})
	if !strings.Contains(text, "任务未完成") || !strings.Contains(text, "将代码写入 sort.py 文件") {
		t.Fatalf("unexpected final: %s", text)
	}
}
