// Package codexadapter translates the Codex CLI's items into Paladin's cards.
// It is pure: it reads Codex's JSON and returns what the conversation should show,
// and knows nothing of turns, persistence or the UI. The runtime drives the turn.
package codexadapter

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/harness"
)

// Name is the harness name a Codex session stores.
const Name = "codex"

// Card maps a Codex item onto a Paladin tool card, or nil when the item has none
// (messages, plans and thinking are text, not cards).
func Card(item codex.Raw) *harness.Card {
	id := item.Str("id")
	if id == "" {
		return nil
	}
	c := &harness.Card{ID: id}
	switch typ := item.Str("type"); typ {
	case "commandExecution":
		c.Name = "shell"
		c.Args = jsonOf(map[string]any{"command": DisplayCommand(item), "cwd": item.Str("cwd")})
	case "fileChange":
		c.Name = "apply_patch"
		files := []map[string]string{}
		for _, ch := range item.List("changes") {
			files = append(files, map[string]string{"path": ch.Str("path"), "change": ch.Obj("kind").Str("type")})
		}
		c.Args = jsonOf(map[string]any{"files": files})
	case "mcpToolCall":
		c.Name = "mcp__" + item.Str("server") + "__" + item.Str("tool")
		c.Args = item.JSON("arguments")
	case "webSearch":
		c.Name = "web_search"
		args := map[string]any{"query": firstNonEmpty(item.Str("query"), item.Obj("action").Str("query"), item.Obj("action").Str("url"), item.Obj("action").Str("pattern"))}
		if kind := item.Obj("action").Str("type"); kind != "" && kind != "search" {
			args["action"] = kind
		}
		c.Args = jsonOf(args)
	case "dynamicToolCall":
		c.Name = item.Str("tool")
		c.Args = item.JSON("arguments")
	case "imageView":
		c.Name = "view_image"
		c.Args = jsonOf(map[string]any{"path": item.Str("path")})
	case "imageGeneration":
		c.Name = "image_generation"
		c.Args = jsonOf(map[string]any{"prompt": item.Str("revisedPrompt")})
	case "subAgentActivity":
		// A subagent Codex started or finished, shown as a short card.
		c.Name = "codex_subagent"
		args := map[string]any{"agent": item.Str("agentPath"), "kind": item.Str("kind")}
		for _, key := range []string{"model", "reasoningEffort", "agentThreadId"} {
			if v := item.Str(key); v != "" {
				args[key] = v
			}
		}
		c.Args = jsonOf(args)
	case "collabAgentToolCall":
		// A subagent Codex spawns or talks to: what it was asked and for what.
		c.Name = "codex_" + firstNonEmpty(item.Str("tool"), "agent")
		args := map[string]any{"agents": item.Strings("receiverThreadIds")}
		for _, key := range []string{"prompt", "model", "reasoningEffort"} {
			if v := item.Str(key); v != "" {
				args[key] = v
			}
		}
		c.Args = jsonOf(args)
	default:
		c.Name = "codex_" + typ
		c.Args = "{}"
	}
	return c
}

// Outcome renders a finished item as the JSON result its card expects. streamed
// is the output Codex sent as it went, used when the item carries none of its own.
func Outcome(item codex.Raw, streamed string) (result string, failed bool) {
	status := item.Str("status")
	if item.Str("type") == "commandExecution" && status == "inProgress" {
		// Asked while it still runs: its outcome is not known, which is not a failure.
		status = "unknown"
	}
	failed = status == "failed" || status == "declined"
	switch item.Str("type") {
	case "commandExecution":
		output := item.Str("aggregatedOutput")
		if output == "" {
			output = streamed
		}
		res := map[string]any{"output": output, "status": status}
		if item.Has("exitCode") {
			res["exit_code"] = item.Int("exitCode")
			failed = failed || item.Int("exitCode") != 0
		}
		if status == "declined" {
			res["error"] = "the user declined this command"
		}
		return jsonOf(res), failed
	case "fileChange":
		var diffs []string
		for _, ch := range item.List("changes") {
			diffs = append(diffs, "--- "+ch.Str("path")+" ("+ch.Obj("kind").Str("type")+")\n"+ch.Str("diff"))
		}
		res := map[string]any{"status": status, "diff": strings.Join(diffs, "\n")}
		if status == "declined" {
			res["error"] = "the user declined these changes"
		}
		return jsonOf(res), failed
	case "mcpToolCall":
		if item.Has("error") {
			return jsonOf(map[string]any{"error": item.Obj("error").Str("message")}), true
		}
		return item.JSON("result"), failed
	case "webSearch":
		return jsonOf(map[string]any{"query": firstNonEmpty(item.Str("query"), item.Obj("action").Str("query")), "action": json.RawMessage(item.JSON("action")), "results": json.RawMessage(item.JSON("results"))}), false
	case "imageGeneration":
		res := map[string]any{"status": status}
		if p := item.Str("savedPath"); p != "" {
			res["saved_path"] = p
		}
		if msg := failureText(item); msg != "" {
			res["error"] = msg
			failed = true
		}
		return jsonOf(res), failed
	case "imageView":
		return jsonOf(map[string]any{"path": item.Str("path")}), false
	case "subAgentActivity":
		return jsonOf(map[string]any{"kind": item.Str("kind")}), false
	case "collabAgentToolCall":
		// Each agent's state, with whatever it reported; an errored or missing
		// one makes the call a failure.
		agents := map[string]any{}
		states := codex.Decode(item["agentsStates"])
		for id := range states {
			state := states.Obj(id)
			agents[id] = map[string]any{"status": state.Str("status"), "message": state.Str("message")}
			if st := state.Str("status"); st == "errored" || st == "notFound" {
				failed = true
			}
		}
		return jsonOf(map[string]any{"status": status, "agents": agents}), failed
	}
	return jsonOf(map[string]any{"status": status}), failed
}

// failureText reads an image generation's failure, which Codex sends as text or an object.
func failureText(item codex.Raw) string {
	if s := item.Str("failure"); s != "" {
		return s
	}
	return item.Obj("failure").Str("message")
}

// PatchCards shows a finished file change the way Paladin shows its own edits: one
// write card per new file and one edit card per changed hunk, so the diff renders
// instead of raw patch text. Each card carries its result.
func PatchCards(item codex.Raw) []*harness.Card {
	var parts []*harness.Card
	for _, ch := range item.List("changes") {
		path, diff := ch.Str("path"), ch.Str("diff")
		switch ch.Obj("kind").Str("type") {
		case "add":
			parts = append(parts, &harness.Card{Name: "write",
				Args:   jsonOf(map[string]any{"path": path, "content": diff}),
				Result: jsonOf(map[string]any{"created": true, "lines": strings.Count(diff, "\n"), "bytes": len(diff)})})
		case "delete":
			parts = append(parts, &harness.Card{Name: "delete", Args: jsonOf(map[string]any{"path": path}), Result: `{"deleted":true}`})
		default:
			hunks := splitHunks(diff)
			if len(hunks) == 0 {
				parts = append(parts, &harness.Card{Name: "apply_patch", Args: jsonOf(map[string]any{"path": path}),
					Result: jsonOf(map[string]any{"diff": diff})})
			}
			for _, h := range hunks {
				parts = append(parts, &harness.Card{Name: "edit",
					Args:   jsonOf(map[string]any{"path": path, "old_string": h.old, "new_string": h.new}),
					Result: `{"replacements":1}`})
			}
		}
	}
	return parts
}

type hunk struct{ old, new string }

// splitHunks reads a unified diff (hunks only, no file header) into the old and new
// text of each hunk.
func splitHunks(diff string) []hunk {
	var out []hunk
	var oldLines, newLines []string
	open := false
	flush := func() {
		if open {
			out = append(out, hunk{old: strings.Join(oldLines, "\n"), new: strings.Join(newLines, "\n")})
		}
		oldLines, newLines, open = nil, nil, false
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			flush()
			open = true
		case !open || strings.HasPrefix(line, "\\"):
		case strings.HasPrefix(line, "-"):
			oldLines = append(oldLines, line[1:])
		case strings.HasPrefix(line, "+"):
			newLines = append(newLines, line[1:])
		case strings.HasPrefix(line, " "):
			oldLines, newLines = append(oldLines, line[1:]), append(newLines, line[1:])
		}
	}
	flush()
	return out
}

// shellWrapper matches the `/bin/zsh -lc '…'` Codex wraps commands in.
var shellWrapper = regexp.MustCompile(`^(?:\S*/)?(?:ba|z|da|k)?sh -l?c (["'])(.*)(["'])$`)

// DisplayCommand is the command a person typed, without the login-shell wrapper
// Codex runs it through.
func DisplayCommand(item codex.Raw) string {
	if actions := item.List("commandActions"); len(actions) == 1 && actions[0].Str("command") != "" {
		return actions[0].Str("command")
	}
	command := item.Str("command")
	if m := shellWrapper.FindStringSubmatch(command); m != nil && m[1] == m[3] {
		return m[2]
	}
	return command
}

// JSONOf encodes v for a card, falling back to an empty object.
func jsonOf(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// JSON is jsonOf for callers outside the package that build card JSON the same way.
func JSON(v any) string { return jsonOf(v) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
