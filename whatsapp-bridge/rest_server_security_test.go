package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewRESTHTTPServerUsesLoopbackAndTimeouts(t *testing.T) {
	server := newRESTHTTPServer(http.NewServeMux(), 8080)

	if server.Addr != "127.0.0.1:8080" {
		t.Fatalf("REST bridge must remain loopback-only, got %q", server.Addr)
	}
	for name, timeout := range map[string]time.Duration{
		"read header": server.ReadHeaderTimeout,
		"read":        server.ReadTimeout,
		"write":       server.WriteTimeout,
		"idle":        server.IdleTimeout,
	} {
		if timeout <= 0 {
			t.Fatalf("%s timeout must be configured", name)
		}
	}
}

func TestSecureRESTHandlerLimitsBodiesAndDisablesCaching(t *testing.T) {
	readFailed := false
	handler := secureRESTHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		readFailed = err != nil
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/send",
		strings.NewReader(strings.Repeat("x", maxRESTRequestBodyBytes+1)),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if !readFailed {
		t.Fatal("oversized request body was not rejected")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected no-store, got %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected nosniff, got %q", response.Header().Get("X-Content-Type-Options"))
	}
}
