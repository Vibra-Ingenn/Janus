package routerconfig

import "strings"

// IsGenericLocalRef reports names that mean "use whatever GGUF is already loaded".
func IsGenericLocalRef(ref string) bool {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "", "local", "default", "janus":
		return true
	default:
		return false
	}
}

// ResolveLocalGGUF returns a .gguf path to load before inference, or "" to keep the resident model.
func (c *Config) ResolveLocalGGUF(modelRef, task string) (ggufPath string, backendID string) {
	modelRef = strings.TrimSpace(modelRef)
	task = strings.TrimSpace(task)

	tryBackend := func(id string) (string, string) {
		id = strings.TrimSpace(id)
		if id == "" || IsGenericLocalRef(id) {
			return "", ""
		}
		if strings.HasSuffix(strings.ToLower(id), ".gguf") {
			return id, ""
		}
		if c == nil {
			return "", id
		}
		entry := c.BackendByID(id)
		if entry == nil {
			return "", ""
		}
		if strings.EqualFold(entry.ID, "local") {
			return "", entry.ID
		}
		if strings.TrimSpace(entry.ModelPath) != "" {
			typ := strings.ToLower(strings.TrimSpace(entry.Type))
			if typ == "" || typ == "vulkan" || typ == "cpu" {
				return entry.ModelPath, entry.ID
			}
		}
		return "", entry.ID
	}

	if !IsGenericLocalRef(modelRef) {
		return tryBackend(modelRef)
	}
	if c != nil && task != "" {
		if backendID := c.ResolveTaskBackend(task); backendID != "" {
			return tryBackend(backendID)
		}
	}
	return "", ""
}
