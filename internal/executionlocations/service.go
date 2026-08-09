// Package executionlocations provides opaque, single-use capabilities that let
// trusted plugin backends choose a chat's execution directory without making
// filesystem paths part of the browser API.
package executionlocations

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	AttributeWorkspacePath = "daw.workspace_path"
	AttributeLocationType  = "daw.execution.type"
	AttributeLocationOwner = "daw.execution.owner"
	AttributeLocationID    = "daw.execution.id"
	LocationType           = "location"
)

var (
	ErrNotFound          = errors.New("execution location not found")
	ErrWorkspaceMismatch = errors.New("execution location belongs to another workspace")
)

type Location struct {
	ID            string
	OwnerPluginID string
	WorkspacePath string
	WorkingDir    string
	ExpiresAt     time.Time
}

func (l Location) Attributes() map[string]string {
	return map[string]string{
		AttributeWorkspacePath: l.WorkspacePath,
		AttributeLocationType:  LocationType,
		AttributeLocationOwner: l.OwnerPluginID,
		AttributeLocationID:    l.ID,
	}
}

type Service struct {
	mu        sync.Mutex
	now       func() time.Time
	locations map[string]Location
}

func New() *Service {
	return &Service{now: time.Now, locations: map[string]Location{}}
}

func (s *Service) Register(ownerPluginID, workspacePath, workingDir string, ttl time.Duration) Location {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	location := Location{
		ID: newID(), OwnerPluginID: ownerPluginID,
		WorkspacePath: filepath.Clean(workspacePath), WorkingDir: filepath.Clean(workingDir),
		ExpiresAt: s.now().Add(ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.locations[location.ID] = location
	return location
}

// Consume atomically claims a capability. A failed workspace check does not
// consume it, allowing the legitimate caller to retry.
func (s *Service) Consume(id, workspacePath string) (Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	location, ok := s.locations[strings.TrimSpace(id)]
	if !ok {
		return Location{}, ErrNotFound
	}
	if location.WorkspacePath != workspacePath {
		return Location{}, ErrWorkspaceMismatch
	}
	delete(s.locations, location.ID)
	return location, nil
}

func (s *Service) pruneLocked() {
	now := s.now()
	for id, location := range s.locations {
		if !location.ExpiresAt.After(now) {
			delete(s.locations, id)
		}
	}
}

func newID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	return "loc_" + hex.EncodeToString(value[:])
}
