package sessionstorebridge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/session"
)

func TestMutationOperationIDsAreIdempotent(t *testing.T) {
	store := session.NewInMemorySessionStore()
	value := session.New(session.WithTitle("test"))
	if err := store.AddSession(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	bridge, err := New(Config{Store: store, Token: "secret", Target: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(bridge)
	defer server.Close()
	payload, _ := json.Marshal(messageRequest{Message: session.UserMessage("hello")})

	var first string
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/store/sessions/"+value.ID+"/messages", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("X-DAW-Operation-ID", "same-operation")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result messageIDResponse
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		encoded := strings.TrimSpace(string(mustJSON(t, result)))
		if i == 0 {
			first = encoded
		} else if encoded != first {
			t.Fatalf("replayed result %s != %s", encoded, first)
		}
	}
	stored, err := store.GetSession(t.Context(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MessageCount() != 1 {
		t.Fatalf("message count = %d, want 1", stored.MessageCount())
	}
}

func TestBridgeValidationAndAuthentication(t *testing.T) {
	bridge, err := New(Config{Store: session.NewInMemorySessionStore(), Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(bridge)
	defer server.Close()

	res, err := http.Get(server.URL + "/v1/store/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", res.StatusCode)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/store/sessions", strings.NewReader(`{"session":null,"unknown":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-DAW-Operation-ID", "malformed")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed status = %d", res.StatusCode)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
