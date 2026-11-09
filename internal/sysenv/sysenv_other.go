//go:build !linux

package sysenv

// The manager only runs on Linux. These let it build and test elsewhere.

func Setup() Info           { return capture() }
func IsRoot() bool          { return false }
func CheckOwnership() error { return nil }
