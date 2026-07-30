package functions

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplyCORSEchoesAllowedOrigins(t *testing.T) {
	origins := []string{"https://needham-circle.github.io", "http://localhost:4000"}

	rec := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "http://localhost:4000")

	if applyCORS(origins, rec, request) {
		t.Fatal("a plain GET should not be treated as handled")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4000" {
		t.Errorf("Allow-Origin = %q, want the request origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

func TestApplyCORSIgnoresUnlistedOrigins(t *testing.T) {
	rec := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "https://evil.example")

	applyCORS([]string{"https://needham-circle.github.io"}, rec, request)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want none for an unlisted origin", got)
	}
}

func TestApplyCORSAnswersPreflight(t *testing.T) {
	rec := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "http://localhost:4000")

	if !applyCORS([]string{"http://localhost:4000"}, rec, request) {
		t.Fatal("preflight should be reported as handled")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
		t.Errorf("Allow-Methods = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
		t.Errorf("Allow-Headers = %q", got)
	}
}
