package runtime

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// An MCP server can stop a turn to ask the user for something: Codex's own app
// approvals ("Allow ChatGPT to use Safari?") arrive this way, as a form. Paladin
// asks it in the question wizard, one question per field, and sends the answers
// back as the form's values.

const (
	persistOnce    = "Just this once"
	persistSession = "For this session"
	persistAlways  = "Always"
)

// elicitField is one value a prompt asks for.
type elicitField struct {
	key      string
	label    string
	kind     string // choice, bool, multi, integer, number or text
	options  []elicitOption
	required bool
}

type elicitOption struct {
	label string
	value any
}

// elicitation answers an MCP server's prompt.
func (c *codexRun) elicitation(m codex.Inbound, p codex.Raw) error {
	respond := func(v map[string]any) error { return c.client.Respond(m.RequestID, v) }
	// A prompt that needs a web page is declined: Paladin does not open links a server hands it.
	if p.Str("mode") == "url" {
		return respond(map[string]any{"action": "decline"})
	}
	message := strings.TrimSpace(p.Str("message"))
	fields := elicitFields(p.Obj("requestedSchema"))
	var asks []tools.AskQuestion
	if len(fields) == 0 {
		// Only a message: a plain allow or decline.
		fields = []elicitField{{kind: "confirm"}}
		asks = append(asks, tools.AskQuestion{Question: orDefault(message, "The server asks for your approval"), Options: []string{"Allow", "Decline"}})
	}
	for i, f := range fields {
		if f.kind == "confirm" {
			continue
		}
		question := f.label
		if i == 0 && message != "" {
			question = message + " — " + f.label
		}
		ask := tools.AskQuestion{Question: question, Multiple: f.kind == "multi"}
		for _, o := range f.options {
			ask.Options = append(ask.Options, o.label)
		}
		if f.kind == "bool" {
			ask.Options = []string{"Yes", "No"}
		}
		if len(ask.Options) > tools.MaxAskOptions {
			ask.Options = ask.Options[:tools.MaxAskOptions]
		}
		asks = append(asks, ask)
	}
	persist := persistChoices(p.Obj("_meta"))
	if len(persist) > 0 {
		asks = append(asks, tools.AskQuestion{Question: "Remember this choice?", Options: append([]string{persistOnce}, persist...)})
	}
	if len(asks) > tools.MaxAskQuestions {
		return respond(map[string]any{"action": "decline"})
	}
	callID := "elicit-" + string(m.RequestID)
	if id := p.Str("elicitationId"); id != "" {
		callID = "elicit-" + id
	}
	answers, err := c.ask(callID, asks)
	if err != nil {
		return respond(map[string]any{"action": "cancel"}) //nolint:nilerr // the stop that dismissed it is handled by stream
	}
	content := map[string]any{}
	for i, f := range fields {
		if f.kind == "confirm" {
			if i < len(answers) && strings.EqualFold(firstAnswer(answers[i]), "Decline") {
				return respond(map[string]any{"action": "decline"})
			}
			continue
		}
		if i >= len(answers) {
			break
		}
		if v, ok := f.value(answers[i]); ok {
			content[f.key] = v
		} else if f.required {
			return respond(map[string]any{"action": "decline"})
		}
	}
	out := map[string]any{"action": "accept", "content": content}
	// The remember question, when there is one, is the last question asked.
	if len(persist) > 0 && len(answers) == len(asks) {
		switch firstAnswer(answers[len(answers)-1]) {
		case persistSession:
			out["_meta"] = map[string]any{"persist": "session"}
		case persistAlways:
			out["_meta"] = map[string]any{"persist": "always"}
		}
	}
	return respond(out)
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func firstAnswer(a tools.AskAnswer) string {
	if len(a.Selected) > 0 {
		return strings.TrimSpace(a.Selected[0])
	}
	return strings.TrimSpace(a.Answer)
}

// persistChoices are the "remember" choices a prompt offers.
func persistChoices(meta codex.Raw) []string {
	var offered []string
	var one string
	switch raw := meta["persist"]; {
	case json.Unmarshal(raw, &offered) == nil:
	case json.Unmarshal(raw, &one) == nil && one != "":
		offered = []string{one}
	}
	var out []string
	for _, want := range []struct{ key, label string }{{"session", persistSession}, {"always", persistAlways}} {
		for _, o := range offered {
			if strings.EqualFold(o, want.key) {
				out = append(out, want.label)
			}
		}
	}
	return out
}

// elicitFields reads a prompt's form, keeping the order the server wrote it in.
func elicitFields(schema codex.Raw) []elicitField {
	required := map[string]bool{}
	for _, k := range schema.Strings("required") {
		required[k] = true
	}
	props := codex.Decode(schema["properties"])
	var fields []elicitField
	for _, key := range orderedKeys(schema["properties"]) {
		prop := props.Obj(key)
		f := elicitField{key: key, label: orDefault(strings.TrimSpace(prop.Str("title")), key), required: required[key]}
		if d := strings.TrimSpace(prop.Str("description")); d != "" {
			f.label += ": " + d
		}
		switch typ := prop.Str("type"); typ {
		case "boolean":
			f.kind = "bool"
		case "array":
			f.kind, f.options = "multi", choicesOf(prop.Obj("items"))
		case "integer", "number":
			f.kind, f.options = typ, choicesOf(prop)
		default:
			f.kind, f.options = "text", choicesOf(prop)
		}
		if len(f.options) > 0 && f.kind != "multi" {
			f.kind = "choice"
		}
		fields = append(fields, f)
	}
	return fields
}

// choicesOf reads a field's fixed choices: an enum (with optional names), or a
// list of const/title pairs.
func choicesOf(prop codex.Raw) []elicitOption {
	var out []elicitOption
	var enum []any
	_ = json.Unmarshal(prop["enum"], &enum)
	names := prop.Strings("enumNames")
	for i, v := range enum {
		label := toString(v)
		if i < len(names) && names[i] != "" {
			label = names[i]
		}
		out = append(out, elicitOption{label: label, value: v})
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		for _, o := range prop.List(key) {
			var v any
			_ = json.Unmarshal(o["const"], &v)
			if v == nil {
				continue
			}
			out = append(out, elicitOption{label: orDefault(strings.TrimSpace(o.Str("title")), toString(v)), value: v})
		}
	}
	return out
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	data, _ := json.Marshal(v)
	return string(data)
}

// value turns the user's answer into the form value.
func (f elicitField) value(a tools.AskAnswer) (any, bool) {
	text := strings.TrimSpace(a.Answer)
	switch f.kind {
	case "choice":
		given := firstAnswer(a)
		for _, o := range f.options {
			if strings.EqualFold(o.label, given) {
				return o.value, true
			}
		}
		return given, given != ""
	case "bool":
		switch strings.ToLower(firstAnswer(a)) {
		case "yes", "true", "allow", "on":
			return true, true
		case "no", "false", "decline", "off":
			return false, true
		}
		return nil, false
	case "multi":
		given := a.Selected
		if len(given) == 0 && text != "" {
			for _, part := range strings.Split(text, ",") {
				given = append(given, strings.TrimSpace(part))
			}
		}
		var out []any
		for _, g := range given {
			var v any = g
			for _, o := range f.options {
				if strings.EqualFold(o.label, g) {
					v = o.value
				}
			}
			out = append(out, v)
		}
		return out, len(out) > 0
	case "integer":
		n, err := strconv.ParseInt(text, 10, 64)
		return n, err == nil
	case "number":
		n, err := strconv.ParseFloat(text, 64)
		return n, err == nil
	}
	return text, text != ""
}

// orderedKeys lists an object's keys in the order they were written.
func orderedKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			return keys
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
}
