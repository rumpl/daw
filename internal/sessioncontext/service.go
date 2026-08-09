// Package sessioncontext manages short-lived opaque capabilities that carry
// session creation provenance across an MCP tool and plugin backend boundary.
package sessioncontext

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

type Context struct {
	ParentChatID string
}

type Service struct {
	mu       sync.RWMutex
	contexts map[string]Context
}

func New() *Service {
	return &Service{contexts: map[string]Context{}}
}

func (s *Service) Issue(context Context) string {
	var value [24]byte
	_, _ = rand.Read(value[:])
	token := "sctx_" + hex.EncodeToString(value[:])
	s.mu.Lock()
	s.contexts[token] = context
	s.mu.Unlock()
	return token
}

func (s *Service) Resolve(token string) (Context, bool) {
	s.mu.RLock()
	context, ok := s.contexts[token]
	s.mu.RUnlock()
	return context, ok
}

func (s *Service) Revoke(token string) {
	s.mu.Lock()
	delete(s.contexts, token)
	s.mu.Unlock()
}
