// Package sysenv sets up the process before anything else runs: it captures
// the few variables the manager needs, then drops the rest of the caller's
// environment, and checks the manager is running safely as root.
package sysenv

import "os"

// Info is what the manager keeps from the caller's environment.
type Info struct {
	SudoUser string // who ran sudo; the login screen's default username
	Term     string
	NoColor  bool
}

func capture() Info {
	_, noColor := os.LookupEnv("NO_COLOR")
	return Info{
		SudoUser: os.Getenv("SUDO_USER"),
		Term:     os.Getenv("TERM"),
		NoColor:  noColor,
	}
}
