package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestNormalizeCreateGroupRequestRequiresExactUniqueParticipants(t *testing.T) {
	_, _, err := normalizeCreateGroupRequest(CreateGroupRequest{
		Name:         "Synthetic group",
		Participants: []string{"Synthetic Contact", "+15555550124"},
	})
	if err == nil {
		t.Fatal("contact-name routing must be rejected")
	}

	_, _, err = normalizeCreateGroupRequest(CreateGroupRequest{
		Name:         "Synthetic group",
		Participants: []string{"+15555550123", "15555550123"},
	})
	if err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("duplicate participants must be rejected, got %v", err)
	}
}

func TestCreateGroupHandlerReturnsOnlySafeGroupMetadata(t *testing.T) {
	called := false
	handler := secureRESTHandler(createGroupHandler(
		func(_ context.Context, name string, participants []types.JID) (types.JID, error) {
			called = true
			if name != "Synthetic group" || len(participants) != 2 {
				t.Fatalf("unexpected normalized request: %q %#v", name, participants)
			}
			return types.NewJID("1234567890-123", types.GroupServer), nil
		},
	))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/groups/create",
		strings.NewReader(`{"name":"Synthetic group","participants":["+15555550123","15555550124@s.whatsapp.net"]}`),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if !called {
		t.Fatal("group creator was not called")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["group_jid"] != "1234567890-123@g.us" || payload["participant_count"] != float64(2) {
		t.Fatalf("unexpected response: %#v", payload)
	}
	if strings.Contains(response.Body.String(), "15555550123") {
		t.Fatal("response must not echo participant identities")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("group-create responses must not be cached")
	}
}
