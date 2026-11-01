package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportFindingsNormalizesAndSorts(t *testing.T) {
	ctx, ws := testCtx(t)
	tool, ok := CommandTool("report_findings")
	if !ok {
		t.Fatal("report_findings is a command tool")
	}
	args, _ := json.Marshal(map[string]any{
		"verdict": "merge_after_fixes",
		"summary": "One real bug.",
		"findings": []map[string]any{
			{"priority": "P2", "title": "Edge case", "path": ws + "/b.go", "start_line": 9, "body": "empty input"},
			{"priority": "p0", "title": "  Nil deref  ", "path": ws + "/a.go", "start_line": 4, "end_line": 6,
				"body": "x may be nil", "fix": "check x", "confidence": "high"},
		},
	})
	out, err := tool.Call(args, ctx)
	if err != nil {
		t.Fatal(err)
	}
	var rep FindingsReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != "merge_after_fixes" || len(rep.Findings) != 2 || rep.Note == "" {
		t.Fatalf("got %+v", rep)
	}
	first := rep.Findings[0]
	if first.ID != "F1" || first.Priority != "P0" || first.Title != "Nil deref" || first.EndLine != 6 || first.Scope != ScopeInside {
		t.Fatalf("P0 first, trimmed and numbered: %+v", first)
	}
	second := rep.Findings[1]
	if second.ID != "F2" || second.EndLine != 9 || second.Confidence != "medium" {
		t.Fatalf("end_line defaults to start_line, confidence to medium: %+v", second)
	}
}

func TestReportFindingsRejectsBadInput(t *testing.T) {
	ctx, ws := testCtx(t)
	tool, _ := CommandTool("report_findings")
	good := map[string]any{"priority": "P1", "title": "t", "path": ws + "/a.go", "start_line": 1, "body": "b"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for key, val := range good {
			m[key] = val
		}
		m[k] = v
		return m
	}
	for name, args := range map[string]map[string]any{
		"bad verdict":   {"verdict": "ship it", "summary": "s", "findings": []any{good}},
		"bad priority":  {"verdict": "needs_rework", "summary": "s", "findings": []any{with("priority", "P9")}},
		"relative path": {"verdict": "needs_rework", "summary": "s", "findings": []any{with("path", "a.go")}},
		"line zero":     {"verdict": "needs_rework", "summary": "s", "findings": []any{with("start_line", 0)}},
		"end before":    {"verdict": "needs_rework", "summary": "s", "findings": []any{with("end_line", 0), with("start_line", 5)}},
		"no title":      {"verdict": "needs_rework", "summary": "s", "findings": []any{with("title", "")}},
	} {
		if name == "end before" {
			args["findings"] = []any{with("start_line", 5)}
			args["findings"].([]any)[0].(map[string]any)["end_line"] = 2
		}
		raw, _ := json.Marshal(args)
		if _, err := tool.Call(raw, ctx); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// No findings with a clean verdict is a valid review.
	raw, _ := json.Marshal(map[string]any{"verdict": "looks_good", "summary": "Nothing found."})
	if out, err := tool.Call(raw, ctx); err != nil || !strings.Contains(out, `"findings":[]`) {
		t.Fatalf("clean review: %s %v", out, err)
	}
	if _, ok := CommandTool("write"); ok {
		t.Fatal("only command tools are offered by name")
	}
}

func TestReportFindingsDerivesAMissingVerdict(t *testing.T) {
	ctx, ws := testCtx(t)
	tool, _ := CommandTool("report_findings")
	for want, findings := range map[string][]any{
		"needs_rework":      {map[string]any{"priority": "P1", "title": "t", "path": ws + "/a.go", "start_line": 1, "body": "b"}},
		"merge_after_fixes": {map[string]any{"priority": "P3", "title": "t", "path": ws + "/a.go", "start_line": 1, "body": "b"}},
		"looks_good":        {},
	} {
		raw, _ := json.Marshal(map[string]any{"findings": findings})
		out, err := tool.Call(raw, ctx)
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		var rep FindingsReport
		_ = json.Unmarshal([]byte(out), &rep)
		if rep.Verdict != want || rep.Summary == "" {
			t.Errorf("want %s with a default summary, got %+v", want, rep)
		}
	}
}
