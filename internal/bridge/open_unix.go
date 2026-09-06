//go:build !windows

package bridge

import (
	"fmt"

	"github.com/ebitengine/purego"
)

// openLib loads a shared library using dlopen and returns its handle.
// The handle is compatible with purego.RegisterLibFunc.
func openLib(path string) (uintptr, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return 0, fmt.Errorf("bridge: dlopen %q: %w", path, err)
	}
	return handle, nil
}

// closeLib releases a shared library handle via dlclose.
func closeLib(handle uintptr) error {
	return purego.Dlclose(handle)
}
