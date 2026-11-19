package runtime

import "encoding/json"

// testReason adds the mandatory "reason" argument to a scripted tool
// call's JSON arguments when they lack one; anything else is unchanged.
func testReason(args string) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &obj) != nil || obj == nil {
		return args
	}
	if _, ok := obj["reason"]; ok {
		return args
	}
	obj["reason"] = json.RawMessage(`"test"`)
	out, err := json.Marshal(obj)
	if err != nil {
		return args
	}
	return string(out)
}
