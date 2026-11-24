package main

import (
	"encoding/json"
	"math"
	"testing"
)

// A result that cannot be encoded must still answer its request id,
// otherwise the client waits for the reply until its deadline.
func TestEncodeResponseAnswersAnUnencodableResult(t *testing.T) {
	id := json.RawMessage(`42`)
	data := encodeResponse(okResponse(id, map[string]float64{"x": math.NaN()}))
	var got struct {
		ID    json.RawMessage `json:"id"`
		OK    bool            `json:"ok"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("not json: %q", data)
	}
	if string(got.ID) != "42" || got.OK || got.Error == "" {
		t.Fatalf("want an error response for id 42, got %s", data)
	}
}

func TestEncodeResponseKeepsAGoodResult(t *testing.T) {
	data := encodeResponse(okResponse(json.RawMessage(`"a"`), map[string]int{"n": 1}))
	if string(data) != `{"id":"a","ok":true,"result":{"n":1}}` {
		t.Fatalf("got %s", data)
	}
}
