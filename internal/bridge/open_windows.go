//go:build windows

package bridge

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modKernel32   = syscall.NewLazyDLL("kernel32.dll")
	procAddDllDir = modKernel32.NewProc("AddDllDirectory")
	procSetDllDir = modKernel32.NewProc("SetDllDirectoryW")
)

// openLib loads a Windows DLL using syscall.LoadDLL and returns its handle.
// It also adds the DLL's directory to the search path so that dependent DLLs
// (like ggml-vulkan.dll, ggml-cpu-*.dll) can be found automatically.
func openLib(path string) (uintptr, error) {
	// Add the directory containing the DLL to the search path.
	// This allows llama.dll to find ggml-vulkan.dll, ggml-cpu-*.dll etc.
	dir := filepath.Dir(path)
	if abs, err := filepath.Abs(dir); err == nil {
		dirPtr, _ := syscall.UTF16PtrFromString(abs)
		// Try AddDllDirectory first (Windows 8+), fall back to SetDllDirectoryW
		if r, _, _ := procAddDllDir.Call(uintptr(unsafe.Pointer(dirPtr))); r == 0 {
			procSetDllDir.Call(uintptr(unsafe.Pointer(dirPtr)))
		}
	}

	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return 0, fmt.Errorf("bridge: LoadDLL %q: %w", path, err)
	}
	return uintptr(dll.Handle), nil
}

// closeLib releases a Windows DLL handle.
func closeLib(handle uintptr) error {
	dll := &syscall.DLL{Handle: syscall.Handle(handle)}
	return dll.Release()
}
