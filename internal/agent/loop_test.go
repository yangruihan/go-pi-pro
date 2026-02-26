package agent

import "testing"

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
