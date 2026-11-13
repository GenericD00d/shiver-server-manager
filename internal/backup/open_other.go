//go:build !unix

package backup

import "os"

// readFlags opens a file to archive. Elsewhere than Unix there are no pipes
// to swap in (see open_unix.go).
const readFlags = os.O_RDONLY
