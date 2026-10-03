//go:build windows

package bridge

import "fmt"

// On the Windows x64 ABI a struct larger than 8 bytes passed by value is
// actually passed as a pointer to a caller-owned copy, so binding the
// pointer-taking signatures directly is ABI-exact.
func (lib *LlamaLib) registerStructFuncs() error {
	for _, s := range []struct {
		ptr  any
		name string
	}{
		{&lib.LoadModelFromFile, "llama_load_model_from_file"},
		{&lib.NewContextWithModel, "llama_new_context_with_model"},
		{&lib.Decode, "llama_decode"},
	} {
		if err := registerSym(s.ptr, lib.handle, s.name); err != nil {
			return fmt.Errorf("bridge: register %q: %w", s.name, err)
		}
	}
	return nil
}
