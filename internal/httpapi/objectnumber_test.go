package httpapi

import (
	"strings"
	"testing"
)

// TestEveryObjectTypeHasItsOwnNumberPrefix keeps objectNumberPrefix in step with
// objectRoutes. payment was routed without a prefix, so every payment was
// numbered OBJ- while every other type carried its own — the fallback is there
// for types the application does not route, not for ones it does.
func TestEveryObjectTypeHasItsOwnNumberPrefix(t *testing.T) {
	for _, route := range objectRoutes {
		prefix, ok := objectNumberPrefix[route.objectType]
		if !ok || prefix == "" {
			t.Errorf("%s is routed at %s but has no number prefix, so it is numbered OBJ-", route.objectType, route.path)
			continue
		}
		number := objectNumber(route.objectType)
		if !strings.HasPrefix(number, prefix+"-") {
			t.Errorf("%s was numbered %q, want the %s- prefix", route.objectType, number, prefix)
		}
	}
	seen := map[string]string{}
	for _, route := range objectRoutes {
		prefix := objectNumberPrefix[route.objectType]
		if other, clash := seen[prefix]; clash {
			t.Errorf("%s and %s share the %s- prefix, so their numbers do not say which is which", other, route.objectType, prefix)
		}
		seen[prefix] = route.objectType
	}
}
