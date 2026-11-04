package routerconfig

import "testing"

func TestResolveLocalGGUF(t *testing.T) {
	cfg := &Config{
		Backends: []BackendEntry{
			{ID: "local", Type: "vulkan", ModelPath: "./models/Lexi.gguf"},
			{ID: "fast", Type: "vulkan", ModelPath: "./models/Vibra-3B.gguf"},
		},
		RoutingRules: map[string]string{
			"proxy_parsing": "fast",
		},
	}

	path, id := cfg.ResolveLocalGGUF("local", "")
	if path != "" || id != "" {
		t.Fatalf("generic local: want resident, got path=%q id=%q", path, id)
	}

	path, id = cfg.ResolveLocalGGUF("", "proxy_parsing")
	if path != "./models/Vibra-3B.gguf" || id != "fast" {
		t.Fatalf("task routing: got path=%q id=%q", path, id)
	}
}
