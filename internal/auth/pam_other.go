//go:build !(linux && cgo)

package auth

import "errors"

// PAM is only available on Linux builds with cgo. This stand-in lets the
// rest of the manager build and test elsewhere.
type PAM struct {
	Service string
	ConfDir string
}

func (PAM) Authenticate(string, Prompter) error {
	return errors.New("PAM login needs a Linux build with cgo")
}
