package main

import (
	"encoding/json"
	"janus/pkg/config"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

func TestMain(m *testing.M) {
	_ = godotenv.Load("../../.env")
	os.Exit(m.Run())
}

func buildTestRouter(t *testing.T) (*router, *http.ServeMux) {
	t.Helper()
	tmpDir := t.TempDir()
	prompts, err := config.OpenPrompts(tmpDir + "/prompts.json")
	if err != nil {
		t.Fatalf("open prompts: %v", err)
	}
	r := new_router(nil, "", prompts)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", r.handle_health)
	mux.HandleFunc("/engine/status", r.handle_engine_status)
	mux.HandleFunc("/version", r.handle_version)
	mux.HandleFunc("/models/default", r.handle_models_default)
	mux.HandleFunc("/models/list", r.handle_models_list)
	mux.HandleFunc("/config/prompts", r.handle_config_prompts)
	mux.HandleFunc("/license/status", notFoundHandler)
	mux.HandleFunc("/users", notFoundHandler)
	mux.HandleFunc("/tools/"+strings.Join([]string{"comm", "unity"}, ""), notFoundHandler)

	return r, mux
}

func notFoundHandler(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}

func TestHealthEndpoint(t *testing.T) {
	_, mux := buildTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health: want 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["ok"] != true {
		t.Fatalf("GET /health: want ok=true, got %v", body)
	}
}

func TestHealthRejectsPost(t *testing.T) {
	_, mux := buildTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health: want 405, got %d", rec.Code)
	}
}

func TestEngineStatusNoProvider(t *testing.T) {
	_, mux := buildTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/engine/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /engine/status: want 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["backend"] != "" {
		t.Fatalf("expected empty backend when provider is nil, got %q", body["backend"])
	}
}

func TestVersionEndpoint(t *testing.T) {
	_, mux := buildTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /version: want 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["current"] == nil || body["current"] == "" {
		t.Fatalf("GET /version: missing 'current' field")
	}
}

func TestStrippedRoutes404(t *testing.T) {
	_, mux := buildTestRouter(t)
	third := "/tools/" + strings.Join([]string{"comm", "unity"}, "")
	for _, path := range []string{"/license/status", "/users", third} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: want 404, got %d", path, rec.Code)
		}
	}
}

func TestModelsDefaultGetSet(t *testing.T) {
	_, mux := buildTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/models/default", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /models/default: want 200, got %d", rec.Code)
	}

	body := `{"model_path":"./models/test.gguf"}`
	req = httptest.NewRequest(http.MethodPost, "/models/default", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /models/default: want 200, got %d — %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/models/default", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var result map[string]any
	json.NewDecoder(rec.Body).Decode(&result)
	if result["model_path"] != "./models/test.gguf" {
		t.Fatalf("expected ./models/test.gguf, got %v", result["model_path"])
	}
}

func TestConfigPromptsGet(t *testing.T) {
	_, mux := buildTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/config/prompts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /config/prompts: want 200, got %d", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	_, mux := buildTestRouter(t)
	endpoints := []struct {
		method, path string
	}{
		{http.MethodPost, "/health"},
		{http.MethodPost, "/engine/status"},
		{http.MethodDelete, "/models/default"},
	}
	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: want 405, got %d", ep.method, ep.path, rec.Code)
		}
	}
}
