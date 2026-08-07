package httpapi

import "sync"

// chatRegistry owns the process-wide live-chat index and enforces the
// single-writer rule for docker-agent sessions. HTTP handlers never manipulate
// its maps directly.
type chatRegistry struct {
	mu        sync.Mutex
	chats     map[string]*liveChat
	bySession map[string]string
}

func newChatRegistry() *chatRegistry {
	return &chatRegistry{
		chats: map[string]*liveChat{}, bySession: map[string]string{},
	}
}

func (r *chatRegistry) get(chatID string) (*liveChat, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	chat, ok := r.chats[chatID]
	return chat, ok
}

func (r *chatRegistry) session(sessionID string) *liveChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chats[r.bySession[sessionID]]
}

// register atomically claims a session. It returns the existing owner when a
// concurrent open won the race.
func (r *chatRegistry) register(sessionID string, chat *liveChat) *liveChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.chats[r.bySession[sessionID]]; existing != nil {
		return existing
	}
	r.chats[chat.id] = chat
	r.bySession[sessionID] = chat.id
	return nil
}

func (r *chatRegistry) remove(chatID string) *liveChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	chat := r.chats[chatID]
	if chat != nil {
		delete(r.chats, chatID)
		delete(r.bySession, chat.chat.SessionID())
	}
	return chat
}

func (r *chatRegistry) all() []*liveChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*liveChat, 0, len(r.chats))
	for _, chat := range r.chats {
		out = append(out, chat)
	}
	return out
}

func (r *chatRegistry) bySessionSnapshot() map[string]*liveChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*liveChat, len(r.bySession))
	for sessionID, chatID := range r.bySession {
		if chat := r.chats[chatID]; chat != nil {
			out[sessionID] = chat
		}
	}
	return out
}
