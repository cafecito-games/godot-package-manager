//go:build !windows

package packager

import "syscall"

// openNoFollow refuses to open a symlink, so a file accepted as a regular file
// by the tree walk cannot be swapped for a link to somewhere else before the
// archive reads it.
const openNoFollow = syscall.O_NOFOLLOW
