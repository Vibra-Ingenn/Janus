package discover

import (
	"syscall"
	"unsafe"
)

const driveFixed = 3 // DRIVE_FIXED: internal disks, not USB sticks, network shares or DVDs

var procGetDriveType = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDriveTypeW")

// scanRootDirs lists the fixed drives (C:\, D:\ …).
func scanRootDirs() []string {
	var roots []string
	for l := 'A'; l <= 'Z'; l++ {
		root := string(l) + `:\`
		p, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if t, _, _ := procGetDriveType.Call(uintptr(unsafe.Pointer(p))); t == driveFixed {
			roots = append(roots, root)
		}
	}
	return roots
}
