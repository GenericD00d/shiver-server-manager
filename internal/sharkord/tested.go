// Package sharkord knows Sharkord itself: its releases, env settings, first
// start, owner token and health endpoint.
package sharkord

import (
	"fmt"
	"strconv"
	"strings"
)

// TestedVersion is a Sharkord release the manager has been checked against,
// with the plugin SDK version it accepts.
type TestedVersion struct {
	Version    string
	PluginSDK  int
	DBSwitches bool // the manager may switch plugins on in the database
}

// Tested lists every version the manager offers, oldest first. Recheck the
// facts in "Prototype first" before adding one.
var Tested = []TestedVersion{
	{Version: "0.0.30", PluginSDK: 2, DBSwitches: true},
}

// LatestTested is the newest version on the list.
func LatestTested() TestedVersion { return Tested[len(Tested)-1] }

// FindTested returns the entry for v, if tested.
func FindTested(v string) (TestedVersion, bool) {
	v = strings.TrimPrefix(v, "v")
	for _, t := range Tested {
		if t.Version == v {
			return t, true
		}
	}
	return TestedVersion{}, false
}

// CompareVersions compares dotted numeric versions ("0.0.30"), ignoring a
// leading "v". Missing parts count as 0.
func CompareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ValidVersion checks a version is plain dotted numbers, so it's safe in paths.
func ValidVersion(v string) error {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return fmt.Errorf("invalid version %q", v)
	}
	for _, p := range parts {
		if p == "" || len(p) > 6 || strings.Trim(p, "0123456789") != "" {
			return fmt.Errorf("invalid version %q", v)
		}
	}
	return nil
}
