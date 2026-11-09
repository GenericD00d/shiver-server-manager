// Package help holds the "Steps outside the manager" text, shown in the
// menu's Help screen and wherever a step comes up.
package help

import (
	_ "embed"
	"strings"
)

//go:embed steps-outside.md
var stepsOutside string

// All is the whole Help text.
func All() string { return stepsOutside }

// Section returns one "## Title" section's text, or "".
func Section(title string) string {
	for _, part := range strings.Split(stepsOutside, "\n## ")[1:] {
		head, body, _ := strings.Cut(part, "\n")
		if strings.EqualFold(strings.TrimSpace(head), title) {
			return strings.TrimSpace(body)
		}
	}
	return ""
}
