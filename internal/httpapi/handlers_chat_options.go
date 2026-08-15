package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/rumpl/daw/internal/chatprefs"
	"github.com/rumpl/daw/internal/protocol"
)

func (s *Server) handleGetChatOptions(w http.ResponseWriter, r *http.Request) {
	options, err := s.resolveChatOptions(r.Context(), s.preferences.Get(""))
	if err != nil {
		s.chatOptionsUnavailable(w, err)
		return
	}
	s.json(w, http.StatusOK, options)
}

func (s *Server) handleUpdateChatOptions(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.UpdateConfigRequest](w, r, s)
	if !ok {
		return
	}
	if req.Model != nil && len(strings.TrimSpace(*req.Model)) > 512 {
		s.fail(w, http.StatusBadRequest, "invalid_model", "the model reference is too long")
		return
	}
	if req.ThinkingLevel != nil && len(strings.TrimSpace(*req.ThinkingLevel)) > 64 {
		s.fail(w, http.StatusBadRequest, "invalid_thinking_level", "the thinking level is too long")
		return
	}

	candidate := s.preferences.Get("")
	if req.Model != nil {
		candidate.Model = strings.TrimSpace(*req.Model)
	}
	if req.ThinkingLevel != nil {
		candidate.ThinkingLevel = strings.TrimSpace(*req.ThinkingLevel)
	}
	options, err := s.resolveChatOptions(r.Context(), candidate)
	if err != nil {
		s.chatOptionsUnavailable(w, err)
		return
	}
	if req.ThinkingLevel != nil && candidate.ThinkingLevel != "" &&
		!slices.Contains(options.ThinkingLevels, candidate.ThinkingLevel) {
		s.fail(w, http.StatusBadRequest, "invalid_thinking_level", "that thinking level is not supported by the selected model")
		return
	}

	// A model change can alter the supported effort vocabulary. If the old
	// default is no longer valid, persist the fallback returned to the client
	// rather than leaving a preference that would make the next chat fail.
	thinkingPatch := req.ThinkingLevel
	if req.Model != nil && req.ThinkingLevel == nil &&
		candidate.ThinkingLevel != "" && !slices.Contains(options.ThinkingLevels, candidate.ThinkingLevel) {
		fallback := options.ThinkingLevel
		thinkingPatch = &fallback
	}
	if _, err := s.preferences.UpdateDefault(req.Model, thinkingPatch); err != nil {
		s.log.Error("update default chat preferences", "error", err)
		s.fail(w, http.StatusInternalServerError, "preference_save_failed", "the default chat settings could not be saved")
		return
	}
	s.json(w, http.StatusOK, options)
}

func (s *Server) handleUpdateDefaultTool(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.UpdateToolRequest](w, r, s)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("tool"))
	if name == "" || len(name) > 256 {
		s.fail(w, http.StatusBadRequest, "invalid_tool", "a valid tool name is required")
		return
	}
	options, err := s.resolveChatOptions(r.Context(), s.preferences.Get(""))
	if err != nil {
		s.chatOptionsUnavailable(w, err)
		return
	}
	if !slices.ContainsFunc(options.Tools, func(tool protocol.ToolOption) bool { return tool.Name == name }) {
		s.fail(w, http.StatusNotFound, "unknown_tool", "unknown tool")
		return
	}
	if err := s.preferences.SetToolEnabled(name, req.Enabled); err != nil {
		s.log.Error("update default tool preference", "error", err)
		s.fail(w, http.StatusInternalServerError, "preference_save_failed", "the tool setting could not be saved")
		return
	}
	// Filtering is global: live runtimes and future runtimes receive the same
	// exclusion set. Each runtime still owns its independent MCP transport.
	preference := s.preferences.Get("")
	for _, chat := range s.chats.all() {
		chat.chat.SetDisabledTools(preference.DisabledTools)
	}
	for _, tool := range options.Tools {
		if tool.Name == name {
			tool.Enabled = req.Enabled
			s.json(w, http.StatusOK, tool)
			return
		}
	}
}

func (s *Server) resolveChatOptions(ctx context.Context, preference chatprefs.Preference) (protocol.ChatOptions, error) {
	models, thinkingLevels, tools, err := s.adapter.ChatOptions(ctx, preference.Model, s.pluginMCPServers())
	if err != nil {
		return protocol.ChatOptions{}, err
	}
	model := preference.Model
	if model == "" {
		for _, option := range models {
			if !option.IsDefault {
				continue
			}
			model = option.Ref
			if option.Provider != "" && option.Model != "" {
				model = option.Provider + "/" + option.Model
			}
			break
		}
	}
	for i := range models {
		models[i].IsCurrent = models[i].Ref == model
	}

	thinkingLevel := preference.ThinkingLevel
	if !slices.Contains(thinkingLevels, thinkingLevel) {
		thinkingLevel = ""
		for _, level := range thinkingLevels {
			if level == "medium" {
				thinkingLevel = level
				break
			}
		}
		if thinkingLevel == "" && len(thinkingLevels) > 0 {
			thinkingLevel = thinkingLevels[0]
		}
	}
	for i := range tools {
		tools[i].Enabled = !slices.Contains(preference.DisabledTools, tools[i].Name)
	}
	slices.SortFunc(tools, func(a, b protocol.ToolOption) int { return strings.Compare(a.Name, b.Name) })
	return protocol.ChatOptions{
		Model: model, ThinkingLevel: thinkingLevel,
		Models: models, ThinkingLevels: thinkingLevels, Tools: tools,
	}, nil
}

func (s *Server) chatOptionsUnavailable(w http.ResponseWriter, err error) {
	s.log.Warn("resolve global chat options failed", "error", err)
	s.fail(w, http.StatusFailedDependency, "chat_options_unavailable", "model options could not be resolved")
}
