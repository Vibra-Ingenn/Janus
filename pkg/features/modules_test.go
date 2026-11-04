package features

import "testing"

func TestCoreModulesAlwaysOn(t *testing.T) {
	for _, m := range []string{"assist", "chat", "config"} {
		if !Enabled(m) {
			t.Fatalf("core module %q should be enabled", m)
		}
	}
}

func TestOptionalNamesOff(t *testing.T) {
	for _, m := range []string{"example-addon", "another-addon"} {
		if Enabled(m) {
			t.Fatalf("connector name %q must not be a built-in module", m)
		}
	}
}

func TestSnapshotEmptyOptional(t *testing.T) {
	snap := Snapshot()
	opt, _ := snap["optional"].(map[string]bool)
	if len(opt) != 0 {
		t.Fatalf("expected no optional modules in snapshot, got %v", opt)
	}
}
