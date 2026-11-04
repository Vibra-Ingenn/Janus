//go:build !windows

package discover

// scanRootDirs is the whole file system; skipDirNames keeps it out of /proc, /sys and /dev.
func scanRootDirs() []string {
	return []string{"/"}
}
