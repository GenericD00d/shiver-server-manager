//go:build unix

package backup

import (
	"os"
	"syscall"
)

// readFlags opens a file to archive without blocking, so a pipe swapped in
// for it between the walk and the open (see addFile) can't hang the backup.
const readFlags = os.O_RDONLY | syscall.O_NONBLOCK
