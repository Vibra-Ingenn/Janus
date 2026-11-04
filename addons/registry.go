package addons

import (
	"net/http"
	"sync"
)

// SidebarItem is one console nav entry from an add-on tool card.
type SidebarItem struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Addon is a host-mounted extension (third-party desk modules, etc.).
type Addon interface {
	ID() string
	Mount(prefix string, mux *http.ServeMux, register func(pattern string, handler http.HandlerFunc))
	Sidebar() []SidebarItem
}

var (
	mu     sync.RWMutex
	addons []Addon
)

// Register adds an add-on. Call from init() in separate add-on modules linked at build time.
func Register(a Addon) {
	if a == nil {
		return
	}
	mu.Lock()
	addons = append(addons, a)
	mu.Unlock()
}

// List returns registered add-ons.
func List() []Addon {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Addon, len(addons))
	copy(out, addons)
	return out
}

// MountAll attaches each add-on under /addons/<id>/.
func MountAll(mux *http.ServeMux) {
	for _, a := range List() {
		id := a.ID()
		prefix := "/addons/" + id + "/"
		reg := func(pattern string, handler http.HandlerFunc) {
			mux.HandleFunc(prefix+pattern, handler)
		}
		a.Mount(prefix, mux, reg)
	}
}

// SidebarItems returns merged sidebar entries from enabled add-ons.
func SidebarItems() []SidebarItem {
	var out []SidebarItem
	for _, a := range List() {
		out = append(out, a.Sidebar()...)
	}
	return out
}
