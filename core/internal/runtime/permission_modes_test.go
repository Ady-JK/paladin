package runtime

import (
	"reflect"
	"testing"
)

func TestPermissionsOfferOnlyFullAccess(t *testing.T) {
	rt, _ := testRuntime(t)
	v, err := rt.PermissionsGet(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Presets, []string{"full-access"}) {
		t.Fatalf("presets = %v", v.Presets)
	}
}
