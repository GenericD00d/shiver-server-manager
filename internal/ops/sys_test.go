package ops

import (
	"reflect"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

// Every hook New sets must be set: tests replace them, so a missing one
// only shows up on a real server.
func TestNewSetsEveryHook(t *testing.T) {
	s := New(&run.Fake{}, state.Default(), nil, "test/1")
	v := reflect.ValueOf(s).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		switch f.Type.Kind() {
		case reflect.Func, reflect.Pointer, reflect.Interface:
			switch f.Name {
			case "Log", "Progress", "VaultKey", "FirewallFor": // set by the caller, or nil on purpose
				continue
			}
			if v.Field(i).IsNil() {
				t.Errorf("New leaves %s nil", f.Name)
			}
		}
	}
}
