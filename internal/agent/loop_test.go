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
