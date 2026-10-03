//go:build !windows

package bridge

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

//go:noinline
func recurse(n int, f func()) {
	var pad [64]byte
	_ = pad
	if n == 0 {
		f()
		return
	}
	recurse(n-1, f)
}

// Regression: llama_*_default_params return a struct through a hidden pointer
// the C side writes to. If that buffer lives on a goroutine stack that grows
// (moves) during the call, the result is lost (n_batch came back 0). Calls
// from fresh goroutines at varying stack depth must match the main goroutine.
// Needs libllama: JANUS_TEST_LIB_DIR or lib/linux of the repo; skipped otherwise.
func TestContextDefaultParamsStableAcrossGoroutines(t *testing.T) {
	dir := os.Getenv("JANUS_TEST_LIB_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "lib", "linux")
	}
	path := filepath.Join(dir, "libllama.so")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("libllama not found at %s", path)
	}
	lib, err := Open(path)
	if err != nil {
		t.Skipf("cannot open %s: %v", path, err)
	}
	defer lib.Close()
	if lib.llamaContextDefaultParamsRaw == nil {
		t.Skip("llama_context_default_params not exported")
	}

	want := lib.ContextDefaultParams(0, 4).NBatch
	if want == 0 {
		t.Fatal("reference n_batch is 0")
	}
	for depth := 0; depth < 200; depth++ {
		var wg sync.WaitGroup
		var got uint32
		wg.Add(1)
		go func() {
			defer wg.Done()
			recurse(depth, func() { got = lib.ContextDefaultParams(0, 4).NBatch })
		}()
		wg.Wait()
		if got != want {
			t.Errorf("depth %d: n_batch = %d, want %d", depth, got, want)
		}
	}
}
