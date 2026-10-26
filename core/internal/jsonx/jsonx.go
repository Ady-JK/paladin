// Package jsonx mirrors serde_json output: compact or indented JSON
// without HTML escaping, so bytes match the Rust core exactly.
package jsonx

import (
	"bytes"
	"encoding/json"
)

func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

func MustMarshal(v any) []byte {
	out, err := Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return out
}

func MarshalIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func MustMarshalIndent(v any) []byte {
	out, err := MarshalIndent(v)
	if err != nil {
		return []byte("{}")
	}
	return out
}
