// coerce.go fixes common model slips in tool arguments against the
// tool's JSON schema before the call runs (and before the gate judges
// it): "20" for an integer, "true" for a boolean, an array or object
// sent as a JSON string, a number where a string belongs, and null
// placeholders for optional fields. Only unambiguous fixes are made;
// anything else is left for the tool to reject with a clear error.
// Numbers are decoded as json.Number, so integers keep every digit.

package tools

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var integerText = regexp.MustCompile(`^-?\d+$`)

// leakedParam is a string that swallowed the next argument as an XML
// tag (`…",\n<parameter name="verdict">needs_rework`), a slip some
// models make when a long string comes before the last field: the
// quote and comma the model meant to close the string with, then one
// tag whose value is a single plain line, at the very end. Valid
// strings can hold the same text, so it is only undone when the call
// is otherwise broken, and never for a tool that changes things (see
// recoverLeaks).
var leakedParam = regexp.MustCompile(`(?s)^(.*)",\s*<parameter name="([A-Za-z_][A-Za-z0-9_]*)">([^<>"\n]+)(?:</parameter>)?\s*$`)

// coerceArgs returns args with slips fixed, or args unchanged when it
// is not an object, the schema says nothing useful, or nothing needed
// fixing (so key order and spacing survive). fixLeaks allows moving a
// leaked tag into its field; it is off for tools that change things,
// where misreading file content would pick what gets written where.
func coerceArgs(args json.RawMessage, schema any, fixLeaks bool) json.RawMessage {
	s, ok := schema.(map[string]any)
	if raw, isRaw := schema.(json.RawMessage); isRaw {
		ok = json.Unmarshal(raw, &s) == nil && s != nil
	}
	if !ok || len(args) == 0 || args[0] != '{' {
		return args
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	if dec.Decode(&obj) != nil || obj == nil || dec.More() {
		return args
	}
	if !coerceObject(obj, s, fixLeaks) {
		return args
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return args
	}
	return out
}

// coerceObject fixes obj's properties in place; it reports a change.
func coerceObject(obj map[string]any, schema map[string]any, fixLeaks bool) bool {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return false
	}
	required := stringList(schema["required"])
	changed := fixLeaks && recoverLeaks(obj, props, required)
	for key, v := range obj {
		prop, ok := props[key].(map[string]any)
		if !ok {
			continue
		}
		if v == nil {
			if !slices.Contains(required, key) && !allowsNull(prop) {
				delete(obj, key)
				changed = true
			}
			continue
		}
		if nv, did := coerceValue(v, prop); did {
			obj[key] = nv
			changed = true
		}
	}
	return changed
}

// recoverLeaks moves a leaked <parameter> tail back into its own field
// when that field is required and missing: only then is the call
// broken as sent. Any other string (file content quoting a tool call,
// say) is kept exactly.
func recoverLeaks(obj map[string]any, props map[string]any, required []string) bool {
	changed := false
	for key, v := range obj {
		s, ok := v.(string)
		if !ok || !strings.Contains(s, "<parameter name=") {
			continue
		}
		m := leakedParam.FindStringSubmatch(s)
		if m == nil || m[1] == "" || strings.Contains(m[1], "<parameter name=") {
			continue
		}
		name := m[2]
		if _, known := props[name]; !known || name == key || !slices.Contains(required, name) {
			continue
		}
		if _, taken := obj[name]; taken {
			continue
		}
		obj[key], obj[name] = strings.TrimRight(m[1], " \t\n"), strings.TrimSpace(m[3])
		changed = true
	}
	return changed
}

// coerceValue fixes one value against its schema.
func coerceValue(v any, schema map[string]any) (any, bool) {
	typ := singleType(schema)
	changed := false
	switch typ {
	case "integer":
		if s, ok := v.(string); ok && integerText.MatchString(strings.TrimSpace(s)) {
			if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				return json.Number(strconv.FormatInt(n, 10)), true
			}
		}
	case "number":
		if s, ok := v.(string); ok {
			if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
				return n, true
			}
		}
	case "boolean":
		if s, ok := v.(string); ok {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		}
	case "string":
		switch x := v.(type) {
		case json.Number:
			return x.String(), true
		case bool:
			return strconv.FormatBool(x), true
		}
	case "array":
		if s, ok := v.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "[") {
			var arr []any
			if decodeNumbers(s, &arr) {
				v, changed = arr, true
			}
		}
		if arr, ok := v.([]any); ok {
			if items, ok := schema["items"].(map[string]any); ok {
				for i, item := range arr {
					if item == nil {
						continue
					}
					if nv, did := coerceValue(item, items); did {
						arr[i] = nv
						changed = true
					}
				}
			}
		}
	case "object":
		if s, ok := v.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
			var obj map[string]any
			if decodeNumbers(s, &obj) && obj != nil {
				v, changed = obj, true
			}
		}
		if obj, ok := v.(map[string]any); ok && coerceObject(obj, schema, false) {
			changed = true
		}
	}
	return v, changed
}

// decodeNumbers decodes JSON text into v keeping numbers as json.Number.
func decodeNumbers(text string, v any) bool {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	return dec.Decode(v) == nil && !dec.More()
}

// singleType is the schema's one non-null type, or "" when it has none
// or several (unions and anyOf are left alone).
func singleType(schema map[string]any) string {
	var types []string
	for _, t := range stringList(schema["type"]) {
		if t != "null" {
			types = append(types, t)
		}
	}
	if len(types) != 1 {
		return ""
	}
	return types[0]
}

func allowsNull(schema map[string]any) bool {
	return slices.Contains(stringList(schema["type"]), "null")
}

// stringList reads a schema keyword that may be a string, []string or a
// decoded []any.
func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
