// Package auth provides a simple local user role system for Janus.
//
// Users are stored in data/users.json (bcrypt-hashed passwords).
// Three roles:
//
//	admin     — full access: manage users, toggle compliance, execute pipelines
//	clinician — submit tasks, view results, read memory; cannot manage users
//	readonly  — view only: health, version, handoff results; no execution
//
// Authentication is HTTP Basic Auth on every request when auth is enabled.
// Auth is disabled when JANUS_AUTH=false (default for local dev).
// Enable with JANUS_AUTH=true and seed the first admin via JANUS_ADMIN_PASSWORD.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Role represents a user's permission level.
type Role string

const (
	RoleAdmin     Role = "admin"
	RoleClinician Role = "clinician"
	RoleReadonly  Role = "readonly"
)

// User represents a single local user record.
type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"` // bcrypt
	Role         Role   `json:"role"`
	Active       bool   `json:"active"`
	APIKey       string `json:"api_key,omitempty"` // for machine-to-machine (Bearer token)
}

// Store holds all users and provides thread-safe access.
type Store struct {
	mu    sync.RWMutex
	users map[string]*User // keyed by username
	path  string
}

var (
	global     *Store
	globalOnce sync.Once
)

// Init initialises the global user store from the given JSON file.
// Seeds an admin user from JANUS_ADMIN_PASSWORD if the file doesn't exist.
func Init(dataDir string) {
	globalOnce.Do(func() {
		path := filepath.Join(dataDir, "users.json")
		s := &Store{path: path, users: make(map[string]*User)}
		if err := s.load(); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("auth: load users: %v", err)
			}
		}
		// Seed default admin if store is empty
		if len(s.users) == 0 {
			pass := os.Getenv("JANUS_ADMIN_PASSWORD")
			if pass == "" {
				pass = randomPassword()
				log.Printf("auth: JANUS_ADMIN_PASSWORD not set — generated admin password. Save it immediately; it will not be shown again.")
			}
			if err := s.AddUser("admin", pass, RoleAdmin); err != nil {
				log.Printf("auth: seed admin: %v", err)
			}
		}
		global = s
	})
}

// Get returns the global store. Always non-nil after Init().
func Get() *Store { return global }

// hashPassword produces a salted HMAC-SHA256 hash: "salt:hex(HMAC(pass,salt))"
func hashPassword(password string) (string, error) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", err
	}
	salt := hex.EncodeToString(saltBytes)
	h := hmac.New(sha256.New, []byte(salt))
	h.Write([]byte(password))
	return salt + ":" + hex.EncodeToString(h.Sum(nil)), nil
}

// checkPassword verifies a password against a stored hash.
func checkPassword(stored, password string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	h := hmac.New(sha256.New, []byte(parts[0]))
	h.Write([]byte(password))
	expected := parts[0] + ":" + hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(stored))
}

// Authenticate returns the user if username/password are valid and user is active.
func (s *Store) Authenticate(username, password string) (*User, bool) {
	s.mu.RLock()
	u, ok := s.users[username]
	s.mu.RUnlock()
	if !ok || !u.Active {
		return nil, false
	}
	if !checkPassword(u.PasswordHash, password) {
		return nil, false
	}
	return u, true
}

// AddUser creates a new user (or updates an existing one).
func (s *Store) AddUser(username, password string, role Role) error {
	hash, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}
	s.mu.Lock()
	s.users[username] = &User{
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		Active:       true,
	}
	s.mu.Unlock()
	return s.save()
}

// SetRole updates an existing user's role.
func (s *Store) SetRole(username string, role Role) error {
	s.mu.Lock()
	u, ok := s.users[username]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("auth: user %q not found", username)
	}
	u.Role = role
	s.mu.Unlock()
	return s.save()
}

// Deactivate disables a user without deleting them.
func (s *Store) Deactivate(username string) error {
	s.mu.Lock()
	u, ok := s.users[username]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("auth: user %q not found", username)
	}
	u.Active = false
	s.mu.Unlock()
	return s.save()
}

// GenerateAPIKey creates a new API key for the given user and persists it.
func (s *Store) GenerateAPIKey(username string) (string, error) {
	s.mu.Lock()
	u, ok := s.users[username]
	if !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("auth: user %q not found", username)
	}
	key := "janus_" + randomPassword() // e.g. janus_a1b2c3d4e5f6...
	u.APIKey = key
	s.mu.Unlock()
	return key, s.save()
}

// AuthenticateByKey returns the user matching the given API key, or nil.
func (s *Store) AuthenticateByKey(key string) (*User, bool) {
	if key == "" {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Active && u.APIKey != "" && u.APIKey == key {
			return u, true
		}
	}
	return nil, false
}

// List returns all users (without password hashes).
func (s *Store) List() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, map[string]any{
			"username": u.Username,
			"role":     string(u.Role),
			"active":   u.Active,
		})
	}
	return out
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var users []*User
	if err := json.Unmarshal(data, &users); err != nil {
		return err
	}
	s.mu.Lock()
	for _, u := range users {
		s.users[u.Username] = u
	}
	s.mu.Unlock()
	return nil
}

func (s *Store) save() error {
	s.mu.RLock()
	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	s.mu.RUnlock()
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func randomPassword() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ─── Middleware ────────────────────────────────────────────────────────────────

// Enabled reports whether auth is turned on via JANUS_AUTH=true.
func Enabled() bool {
	v := os.Getenv("JANUS_AUTH")
	return v == "true" || v == "1"
}

// Middleware wraps a handler with Basic Auth enforcement.
// When auth is disabled (JANUS_AUTH not set) the handler runs without checks.
// minRole: the minimum role required. RoleReadonly < RoleClinician < RoleAdmin.
func Middleware(minRole Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !Enabled() || global == nil {
			next(w, r)
			return
		}
		var user *User
		// Try Bearer token (API key) first
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			key := strings.TrimPrefix(auth, "Bearer ")
			u, ok := global.AuthenticateByKey(key)
			if !ok {
				http.Error(w, "invalid api key", http.StatusUnauthorized)
				return
			}
			user = u
		} else {
			// Fall back to Basic Auth
			username, password, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="Janus"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			u, authed := global.Authenticate(username, password)
			if !authed {
				w.Header().Set("WWW-Authenticate", `Basic realm="Janus"`)
				http.Error(w, "invalid credentials", http.StatusUnauthorized)
				return
			}
			user = u
		}
		if !roleAtLeast(user.Role, minRole) {
			http.Error(w, "insufficient role", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// roleAtLeast returns true when role >= minimum.
func roleAtLeast(role, minimum Role) bool {
	order := map[Role]int{RoleReadonly: 0, RoleClinician: 1, RoleAdmin: 2}
	return order[role] >= order[minimum]
}

// CurrentUser extracts the authenticated user from the request.
// Returns ("", RoleReadonly, false) when auth is disabled or creds are missing/invalid.
// When auth is disabled it returns ("anonymous", RoleAdmin, true).
func CurrentUser(r *http.Request) (username string, role Role, ok bool) {
	if !Enabled() || global == nil {
		return "anonymous", RoleAdmin, true
	}
	// Try Bearer token first
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		key := strings.TrimPrefix(auth, "Bearer ")
		u, found := global.AuthenticateByKey(key)
		if found {
			return u.Username, u.Role, true
		}
		return "", RoleReadonly, false
	}
	// Fall back to Basic Auth
	user, pass, have := r.BasicAuth()
	if !have {
		return "", RoleReadonly, false
	}
	u, authed := global.Authenticate(user, pass)
	if !authed {
		return "", RoleReadonly, false
	}
	return u.Username, u.Role, true
}

// HandleMe is a handler for GET /auth/me — returns the current user's role.
func HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username, role, ok := CurrentUser(r)
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"authenticated":false,"role":"readonly"}`)
		return
	}
	fmt.Fprintf(w, `{"authenticated":true,"username":%q,"role":%q,"auth_enabled":%v}`,
		username, string(role), Enabled())
}
