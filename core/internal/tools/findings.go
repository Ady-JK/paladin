// findings.go holds report_findings, the structured end of a code
// review. It is a command tool: never in the normal registry, only
// offered in the turn of a prompt command whose frontmatter names it
// (the built-in /review). The model records typed findings once; the
// UI draws them as a card from the call's result, so they survive in
// the transcript like any tool row.

package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	maxFindings     = 50
	maxFindingTitle = 200
	maxFindingText  = 4000
)

var (
	findingPriorities = []string{"P0", "P1", "P2", "P3"}
	findingVerdicts   = []string{"looks_good", "merge_after_fixes", "needs_rework"}
	findingConfidence = []string{"high", "medium", "low"}
)

// commandTools are tools a prompt command can switch on for its turn.
var commandTools = map[string]func() Tool{
	"report_findings": func() Tool { return ReportFindingsTool{} },
}

// CommandTool returns a fresh command-only tool by name.
func CommandTool(name string) (Tool, bool) {
	ctor, ok := commandTools[name]
	if !ok {
		return nil, false
	}
	return ctor(), true
}

// Finding is one review issue as recorded.
type Finding struct {
	ID         string `json:"id"`
	Priority   string `json:"priority"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Scope      string `json:"scope"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Body       string `json:"body"`
	Fix        string `json:"fix,omitempty"`
	Confidence string `json:"confidence"`
}

// FindingsReport is report_findings' result, which the UI renders.
type FindingsReport struct {
	Verdict  string    `json:"verdict"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
	Note     string    `json:"note"`
}

type ReportFindingsTool struct{}

func (ReportFindingsTool) Name() string { return "report_findings" }

func (ReportFindingsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "report_findings",
		Description: "Record the result of this code review: every real issue as a typed finding plus an overall verdict. " +
			"Call it once, after you have read the changes and the code around them. The user sees the findings as a card " +
			"with links into the code, so afterwards end with a one-line summary instead of repeating them.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"verdict": map[string]any{"type": "string", "enum": findingVerdicts, "description": "looks_good, merge_after_fixes or needs_rework."},
				"summary": map[string]any{"type": "string", "description": "One or two sentences on the change overall."},
				"findings": map[string]any{
					"type":     "array",
					"maxItems": maxFindings,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"priority":   map[string]any{"type": "string", "enum": findingPriorities, "description": "P0 must fix / will break, P1 likely bug, P2 edge case or risk, P3 minor."},
							"title":      map[string]any{"type": "string", "description": "Short statement of the problem."},
							"path":       map[string]any{"type": "string", "description": "Absolute path of the file."},
							"start_line": map[string]any{"type": "integer", "minimum": 1, "description": "First line of the problem."},
							"end_line":   map[string]any{"type": "integer", "minimum": 1, "description": "Last line; defaults to start_line."},
							"body":       map[string]any{"type": "string", "description": "What is wrong and the concrete failure scenario."},
							"fix":        map[string]any{"type": "string", "description": "The suggested fix."},
							"confidence": map[string]any{"type": "string", "enum": findingConfidence, "description": "How sure you are. Defaults to medium."},
						},
						"required": []string{"priority", "title", "path", "start_line", "body"},
					},
				},
			},
			"required": []string{"verdict", "summary", "findings"},
		},
	}
}

func (ReportFindingsTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Verdict  string    `json:"verdict"`
		Summary  string    `json:"summary"`
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("report_findings: the arguments are not valid JSON: %v", err))
	}
	rep := FindingsReport{
		Verdict:  strings.ToLower(strings.TrimSpace(args.Verdict)),
		Summary:  clipText(strings.TrimSpace(args.Summary), maxFindingText),
		Findings: []Finding{},
	}
	if rep.Verdict != "" && !contains(findingVerdicts, rep.Verdict) {
		return "", ArgsError(fmt.Sprintf("report_findings: verdict must be one of %s", strings.Join(findingVerdicts, ", ")))
	}
	if len(args.Findings) > maxFindings {
		return "", ArgsError(fmt.Sprintf("report_findings: at most %d findings; keep the most important", maxFindings))
	}
	workspace := ""
	if ctx != nil {
		workspace = ctx.Workspace
	}
	for i, f := range args.Findings {
		f.Priority = strings.ToUpper(strings.TrimSpace(f.Priority))
		f.Title = clipText(strings.TrimSpace(f.Title), maxFindingTitle)
		f.Body = clipText(strings.TrimSpace(f.Body), maxFindingText)
		f.Fix = clipText(strings.TrimSpace(f.Fix), maxFindingText)
		f.Confidence = strings.ToLower(strings.TrimSpace(f.Confidence))
		where := fmt.Sprintf("report_findings: finding %d", i+1)
		switch {
		case !contains(findingPriorities, f.Priority):
			return "", ArgsError(where + ": priority must be P0, P1, P2 or P3")
		case f.Title == "" || f.Body == "":
			return "", ArgsError(where + ": title and body are required")
		case f.StartLine < 1:
			return "", ArgsError(where + ": start_line must be 1 or more")
		}
		if f.EndLine == 0 {
			f.EndLine = f.StartLine
		}
		if f.EndLine < f.StartLine {
			return "", ArgsError(where + ": end_line is before start_line")
		}
		if f.Confidence == "" {
			f.Confidence = "medium"
		}
		if !contains(findingConfidence, f.Confidence) {
			return "", ArgsError(where + ": confidence must be high, medium or low")
		}
		path, scope, err := ResolveScopedPath("report_findings", "path", f.Path, workspace)
		if err != nil {
			return "", ArgsError(where + ": " + err.Error())
		}
		f.Path, f.Scope = path, scope
		rep.Findings = append(rep.Findings, f)
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool { return rep.Findings[i].Priority < rep.Findings[j].Priority })
	// A verdict or summary lost on the way is derived, not refused: the
	// findings are what matter.
	if rep.Verdict == "" {
		rep.Verdict = derivedVerdict(rep.Findings)
	}
	if rep.Summary == "" {
		rep.Summary = fmt.Sprintf("%d findings.", len(rep.Findings))
	}
	for i := range rep.Findings {
		rep.Findings[i].ID = fmt.Sprintf("F%d", i+1)
	}
	rep.Note = fmt.Sprintf("Recorded %d findings; the user sees them as a card. End your turn with a one-line summary.", len(rep.Findings))
	out, err := json.Marshal(rep)
	return string(out), err
}

// derivedVerdict is the verdict the worst finding implies.
func derivedVerdict(findings []Finding) string {
	switch {
	case len(findings) == 0:
		return "looks_good"
	case findings[0].Priority <= "P1":
		return "needs_rework"
	default:
		return "merge_after_fixes"
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
