package ginspa

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestServeSPA(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockFS := fstest.MapFS{
		"index.html": {
			Data: []byte("<!DOCTYPE html><html><body>App</body></html>"),
		},
		"assets/app.js": {
			Data: []byte("console.log('hello');"),
		},
	}

	router := gin.New()
	Serve(router, mockFS)

	// Test 1: Root request -> index.html
	req1, _ := http.NewRequest(http.MethodGet, "/", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("expected status 200 for /, got %d", w1.Code)
	}
	if w1.Header().Get("Cache-Control") != "no-cache, no-store, must-revalidate" {
		t.Errorf("expected no-cache header, got %s", w1.Header().Get("Cache-Control"))
	}

	// Test 2: SPA client route -> index.html fallback
	req2, _ := http.NewRequest(http.MethodGet, "/dashboard/nodes/123", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected status 200 for /dashboard/nodes/123, got %d", w2.Code)
	}

	// Test 3: API route missing -> 404 JSON
	req3, _ := http.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)

	if w3.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for /api/v1/unknown, got %d", w3.Code)
	}
}
