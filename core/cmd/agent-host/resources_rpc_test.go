package main

import (
	"encoding/json"
	"testing"
)

func TestResourcesReportsTheCoreAndASessionMap(t *testing.T) {
	resp := callUnary(testRuntime(t), "1", "resources", nil)
	mustOK(t, resp)
	data, _ := json.Marshal(resp.Result)
	var snap struct {
		Supported bool `json:"supported"`
		At        int64
		Self      struct {
			RSS int64 `json:"rss"`
		} `json:"self"`
		Sessions map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Sessions == nil {
		t.Fatalf("sessions should be an object: %s", data)
	}
	if snap.Supported && snap.Self.RSS <= 0 {
		t.Fatalf("supported snapshot without the core's rss: %s", data)
	}
}
