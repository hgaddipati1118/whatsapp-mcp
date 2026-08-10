package main

import (
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPairingStatusDoesNotExposeRawCode(t *testing.T) {
	state := &pairingState{}
	state.update("waiting_for_scan", "synthetic-pairing-code")
	request := httptest.NewRequest(http.MethodGet, "/pairing/status", nil)
	response := httptest.NewRecorder()

	newPairingHandler(state).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "waiting_for_scan" || payload["qr_available"] != true {
		t.Fatalf("unexpected status payload: %#v", payload)
	}
	if _, found := payload["code"]; found {
		t.Fatal("pairing status must not expose the raw QR code")
	}
}

func TestPairingQRReturnsNoStorePNG(t *testing.T) {
	state := &pairingState{}
	state.update("waiting_for_scan", "synthetic-pairing-code")
	request := httptest.NewRequest(http.MethodGet, "/pairing/qr.png", nil)
	response := httptest.NewRecorder()

	newPairingHandler(state).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected no-store, got %q", response.Header().Get("Cache-Control"))
	}
	if _, err := png.Decode(response.Body); err != nil {
		t.Fatalf("expected valid PNG: %v", err)
	}
}

func TestPairingQRIsUnavailableBeforeCode(t *testing.T) {
	state := &pairingState{status: "starting"}
	request := httptest.NewRequest(http.MethodGet, "/pairing/qr.png", nil)
	response := httptest.NewRecorder()

	newPairingHandler(state).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", response.Code)
	}
}

func TestConsolePairingQRIsOptIn(t *testing.T) {
	t.Setenv("WHATSAPP_PAIRING_CONSOLE_QR", "")
	if consolePairingQREnabled() {
		t.Fatal("console pairing QR must be disabled by default")
	}
	t.Setenv("WHATSAPP_PAIRING_CONSOLE_QR", "1")
	if !consolePairingQREnabled() {
		t.Fatal("console pairing QR should require an explicit opt-in")
	}
}
