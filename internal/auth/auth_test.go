package auth

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	return &Store{path: path, users: make(map[string]*User)}
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := hashPassword("mysecret")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword(hash, "mysecret") {
		t.Error("checkPassword should return true for correct password")
	}
	if checkPassword(hash, "wrongpassword") {
		t.Error("checkPassword should return false for wrong password")
	}
}

func TestHashPasswordUnique(t *testing.T) {
	h1, _ := hashPassword("same")
	h2, _ := hashPassword("same")
	if h1 == h2 {
		t.Error("two hashes of the same password should differ (different salts)")
	}
}

func TestCheckPasswordBadFormat(t *testing.T) {
	if checkPassword("no-colon-here", "pass") {
		t.Error("should return false for malformed hash")
	}
}

func TestAddUserAndAuthenticate(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddUser("alice", "pass123", RoleClinician); err != nil {
		t.Fatal(err)
	}

	u, ok := s.Authenticate("alice", "pass123")
	if !ok || u == nil {
		t.Fatal("authentication should succeed")
	}
	if u.Role != RoleClinician {
		t.Errorf("role = %s, want clinician", u.Role)
	}
}

func TestAuthenticateWrongPassword(t *testing.T) {
	s := newTestStore(t)
	s.AddUser("bob", "correct", RoleAdmin)

	_, ok := s.Authenticate("bob", "wrong")
	if ok {
		t.Error("should not authenticate with wrong password")
	}
}

func TestAuthenticateUnknownUser(t *testing.T) {
	s := newTestStore(t)
	_, ok := s.Authenticate("ghost", "anything")
	if ok {
		t.Error("should not authenticate unknown user")
	}
}

func TestSetRole(t *testing.T) {
	s := newTestStore(t)
	s.AddUser("carol", "pass", RoleReadonly)

	if err := s.SetRole("carol", RoleAdmin); err != nil {
		t.Fatal(err)
	}

	u, ok := s.Authenticate("carol", "pass")
	if !ok {
		t.Fatal("auth should succeed after role change")
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %s, want admin", u.Role)
	}
}

func TestSetRoleUnknownUser(t *testing.T) {
	s := newTestStore(t)
	err := s.SetRole("nobody", RoleAdmin)
	if err == nil {
		t.Error("SetRole should fail for unknown user")
	}
}

func TestDeactivate(t *testing.T) {
	s := newTestStore(t)
	s.AddUser("dave", "pass", RoleClinician)

	if err := s.Deactivate("dave"); err != nil {
		t.Fatal(err)
	}

	_, ok := s.Authenticate("dave", "pass")
	if ok {
		t.Error("deactivated user should not authenticate")
	}
}

func TestListUsers(t *testing.T) {
	s := newTestStore(t)
	s.AddUser("a", "p", RoleAdmin)
	s.AddUser("b", "p", RoleClinician)

	users := s.List()
	if len(users) != 2 {
		t.Errorf("List() returned %d users, want 2", len(users))
	}
}

func TestGenerateAPIKey(t *testing.T) {
	s := newTestStore(t)
	s.AddUser("apiuser", "pass", RoleClinician)

	key, err := s.GenerateAPIKey("apiuser")
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Error("API key should not be empty")
	}
	if len(key) < 10 {
		t.Error("API key should be reasonably long")
	}
}

func TestGenerateAPIKeyUnknownUser(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GenerateAPIKey("ghost")
	if err == nil {
		t.Error("should error for unknown user")
	}
}

func TestAuthenticateByKey(t *testing.T) {
	s := newTestStore(t)
	s.AddUser("machine", "pass", RoleAdmin)

	key, _ := s.GenerateAPIKey("machine")
	u, ok := s.AuthenticateByKey(key)
	if !ok || u == nil {
		t.Fatal("should authenticate by API key")
	}
	if u.Username != "machine" {
		t.Errorf("username = %q, want machine", u.Username)
	}
}

func TestAuthenticateByKeyInvalid(t *testing.T) {
	s := newTestStore(t)
	_, ok := s.AuthenticateByKey("bogus_key")
	if ok {
		t.Error("should not authenticate with invalid key")
	}
}

func TestAuthenticateByKeyEmpty(t *testing.T) {
	s := newTestStore(t)
	_, ok := s.AuthenticateByKey("")
	if ok {
		t.Error("should not authenticate with empty key")
	}
}

func TestAuthenticateByKeyDeactivated(t *testing.T) {
	s := newTestStore(t)
	s.AddUser("ex", "pass", RoleClinician)
	key, _ := s.GenerateAPIKey("ex")
	s.Deactivate("ex")

	_, ok := s.AuthenticateByKey(key)
	if ok {
		t.Error("deactivated user should not authenticate by API key")
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")

	// Create and save
	s1 := &Store{path: path, users: make(map[string]*User)}
	s1.AddUser("persist", "pass", RoleAdmin)

	// Load fresh
	s2 := &Store{path: path, users: make(map[string]*User)}
	if err := s2.load(); err != nil {
		t.Fatal(err)
	}

	u, ok := s2.Authenticate("persist", "pass")
	if !ok || u == nil {
		t.Error("user should persist across loads")
	}
}
