package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rumpl/daw/internal/protocol"
)

func TestModelsGatewaySetting(t *testing.T) {
	h := newHarness(t)

	initialResponse := h.do(http.MethodGet, "/api/settings/models-gateway", nil)
	t.Cleanup(func() { initialResponse.Body.Close() })
	initial := decodeJSON[protocol.ModelsGatewayConfig](t, initialResponse)
	if initial.URL != "" {
		t.Fatalf("initial gateway URL = %q, want empty", initial.URL)
	}

	updatedResponse := h.do(http.MethodPut, "/api/settings/models-gateway",
		protocol.UpdateModelsGatewayRequest{URL: "  https://ai-backend-service.docker.com/proxy/  "})
	t.Cleanup(func() { updatedResponse.Body.Close() })
	updated := decodeJSON[protocol.ModelsGatewayConfig](t, updatedResponse)
	const want = "https://ai-backend-service.docker.com/proxy"
	if updated.URL != want {
		t.Fatalf("updated gateway URL = %q, want %q", updated.URL, want)
	}
	stored, err := h.fake.ModelsGateway(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stored != want {
		t.Fatalf("adapter gateway URL = %q, want %q", stored, want)
	}

	clearedResponse := h.do(http.MethodPut, "/api/settings/models-gateway",
		protocol.UpdateModelsGatewayRequest{URL: ""})
	t.Cleanup(func() { clearedResponse.Body.Close() })
	cleared := decodeJSON[protocol.ModelsGatewayConfig](t, clearedResponse)
	if cleared.URL != "" {
		t.Fatalf("cleared gateway URL = %q, want empty", cleared.URL)
	}
}

func TestModelsGatewayRejectsUnsafeOrInvalidURLs(t *testing.T) {
	h := newHarness(t)
	for _, raw := range []string{
		"gateway.example.com",
		"ftp://gateway.example.com",
		"https://user:secret@gateway.example.com",
		"https://gateway.example.com/proxy#fragment",
		"https://",
		"https://gateway.example.com/" + strings.Repeat("x", 2048),
	} {
		resp := h.do(http.MethodPut, "/api/settings/models-gateway", protocol.UpdateModelsGatewayRequest{URL: raw})
		t.Cleanup(func() { resp.Body.Close() })
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("gateway URL %q status = %d, want %d", raw, resp.StatusCode, http.StatusBadRequest)
		}
	}
}
