package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/rumpl/daw/internal/protocol"
)

const maxModelsGatewayURLLength = 2048

var errInvalidGatewayURL = errors.New("invalid models gateway URL")

func (s *Server) handleGetModelsGateway(w http.ResponseWriter, r *http.Request) {
	gatewayURL, err := s.adapter.ModelsGateway(r.Context())
	if err != nil {
		s.log.Warn("read models gateway setting failed", "error", err)
		s.fail(w, http.StatusInternalServerError, "gateway_setting_unavailable", "the LLM gateway setting could not be loaded")
		return
	}
	s.json(w, http.StatusOK, protocol.ModelsGatewayConfig{URL: gatewayURL})
}

func (s *Server) handlePutModelsGateway(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.UpdateModelsGatewayRequest](w, r, s)
	if !ok {
		return
	}
	gatewayURL, err := normalizeModelsGatewayURL(req.URL)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_gateway_url", "the LLM gateway must be an absolute HTTP or HTTPS URL without credentials or a fragment")
		return
	}
	if err := s.adapter.SetModelsGateway(r.Context(), gatewayURL); err != nil {
		s.log.Error("update models gateway setting", "error", err)
		s.fail(w, http.StatusInternalServerError, "gateway_setting_save_failed", "the LLM gateway setting could not be saved")
		return
	}
	s.json(w, http.StatusOK, protocol.ModelsGatewayConfig{URL: gatewayURL})
}

func normalizeModelsGatewayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxModelsGatewayURLLength {
		return "", errInvalidGatewayURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		if err != nil {
			return "", err
		}
		return "", errInvalidGatewayURL
	}
	// Match docker-agent's CLI canonicalization so the same config value is
	// produced whether it is saved from the terminal or the dashboard.
	return strings.TrimSuffix(raw, "/"), nil
}
