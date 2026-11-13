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

// Tested lists the versions whose facts were checked by hand, oldest first.
// It doesn't limit what the manager offers: that is every published Sharkord
// release (Client.Releases). A version newer than the list gets the newest
// entry's facts (FactsFor), so add one when a fact changes, or to mark a
// version as checked. Recheck the facts in "Prototype first" when you do.
var Tested = []TestedVersion{
	{Version: "0.0.30", PluginSDK: 2, DBSwitches: true},
	{Version: "0.0.31", PluginSDK: 2, DBSwitches: true},
}

// LatestTested is the newest version on the list.
func LatestTested() TestedVersion { return Tested[len(Tested)-1] }

// FactsFor returns what the manager assumes about version v: its own entry
// when it was checked, otherwise that of the newest checked version older
// than v (or the oldest entry, for a version older than all of them). A
// wrong plugin SDK guess is caught: a plugin's manifest is checked against
// it, Sharkord refuses a plugin built for another SDK, and an install that
// doesn't load is rolled back.
func FactsFor(v string) TestedVersion {
	if t, ok := FindTested(v); ok {
		return t
	}
	facts := Tested[0]
	for _, t := range Tested {
		if CompareVersions(t.Version, v) <= 0 {
			facts = t
		}
	}
	facts.Version = strings.TrimPrefix(v, "v")
	return facts
}

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
