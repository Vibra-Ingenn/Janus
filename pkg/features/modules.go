package features

import (
	"os"
	"strings"

	"janus/pkg/discover"
	"janus/pkg/singleton"
)

// Core UI surfaces always shipped with Janus. Other products connect over HTTP; they are not in this binary.
var coreModules = map[string]bool{
	"assist": true,
	"chat":   true,
	"config": true,
}

// Enabled reports whether a named UI module is on. Only core modules exist in public Janus.
func Enabled(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return coreModules[name]
}

// Snapshot is returned by GET /config/features for the web UI.
func Snapshot() map[string]any {
	cap := discover.LastSnapshot()
	return map[string]any{
		"core":             coreModules,
		"optional":         map[string]bool{},
		"defer_model_load": DeferModelLoad(),
		"build":            strings.TrimSpace(os.Getenv("JANUS_BUILD")),
		"install_root":     singleton.InstallRoot(),
		"capabilities":     cap,
	}
}

// DeferModelLoad skips loading JANUS_MODEL_PATH at startup (load via UI or POST /models/load).
func DeferModelLoad() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_DEFER_MODEL_LOAD")))
	return v == "1" || v == "true" || v == "yes"
}
