package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
