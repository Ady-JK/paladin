package mcp

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxToolName is the strictest provider limit on function names.
const maxToolName = 64

// maxResultChars caps what one MCP call hands the model.
const maxResultChars = 40000

// exposedName is the model-facing name: mcp__<server>__<tool>, limited
// to [A-Za-z0-9_-]. Over-long names keep a readable prefix plus a hash
// of the full name, so distinct tools stay distinct.
func exposedName(server, tool string) string {
	name := "mcp__" + sanitizeName(server) + "__" + sanitizeName(tool)
	if len(name) <= maxToolName {
		return name
	}
	sum := sha1.Sum([]byte(server + "\x00" + tool))
	suffix := "_" + hex.EncodeToString(sum[:])[:8]
	return name[:maxToolName-len(suffix)] + suffix
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// sanitizeSchema turns a server's inputSchema into something every
// provider accepts: an object schema with properties and no $schema.
func sanitizeSchema(raw json.RawMessage) map[string]any {
	var schema map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil || schema == nil {
		schema = map[string]any{}
	}
	delete(schema, "$schema")
	schema["type"] = "object"
	if _, ok := schema["properties"].(map[string]any); !ok {
		schema["properties"] = map[string]any{}
	}
	return schema
}

// renderResult flattens a call result into the text the model reads.
// Binary blocks are described rather than inlined.
func renderResult(res CallResult) string {
	var parts []string
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "image", "audio":
			parts = append(parts, fmt.Sprintf("[%s %s, %d bytes]", c.Type, c.MimeType, decodedLen(c.Data)))
		case "resource_link":
			label := c.Name
			if label == "" {
				label = c.URI
			}
			parts = append(parts, fmt.Sprintf("[resource link %s: %s]", label, c.URI))
		case "resource":
			if r := c.Resource; r != nil {
				if r.Text != "" {
					parts = append(parts, fmt.Sprintf("[resource %s]\n%s", r.URI, r.Text))
				} else {
					parts = append(parts, fmt.Sprintf("[resource %s %s, %d bytes]", r.URI, r.MimeType, decodedLen(r.Blob)))
				}
			}
		default:
			parts = append(parts, fmt.Sprintf("[%s content]", c.Type))
		}
	}
	out := strings.Join(parts, "\n\n")
	if strings.TrimSpace(out) == "" && len(res.StructuredContent) > 0 {
		out = string(res.StructuredContent)
	}
	if strings.TrimSpace(out) == "" {
		return "(no output)"
	}
	return clip(out, maxResultChars)
}

func decodedLen(b64 string) int {
	if n := base64.StdEncoding.DecodedLen(len(b64)); n > 0 {
		if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
			return len(raw)
		}
		return n
	}
	return 0
}

// clip keeps the head and tail of an over-long result.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	head, from := max*3/4, len(s)-max/4
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for from < len(s) && !utf8.RuneStart(s[from]) {
		from++
	}
	return s[:head] + fmt.Sprintf("\n\n[… %d characters omitted …]\n\n", from-head) + s[from:]
}
