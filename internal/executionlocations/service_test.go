package executionlocations

import (
	"errors"
	"testing"
	"time"
)

func TestCapabilitiesAreSingleUseAndWorkspaceScoped(t *testing.T) {
	service := New()
	location := service.Register("plugin", "/project", "/checkouts/one", time.Minute)
	if _, err := service.Consume(location.ID, "/other"); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("wrong workspace error = %v", err)
	}
	consumed, err := service.Consume(location.ID, "/project")
	if err != nil {
		t.Fatal(err)
	}
	if consumed.WorkingDir != "/checkouts/one" || consumed.OwnerPluginID != "plugin" {
		t.Fatalf("unexpected location: %+v", consumed)
	}
	if _, err := service.Consume(location.ID, "/project"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second consume error = %v", err)
	}
}

func TestExpiredCapabilityIsRejected(t *testing.T) {
	service := New()
	now := time.Now()
	service.now = func() time.Time { return now }
	location := service.Register("plugin", "/project", "/checkout", time.Second)
	now = now.Add(2 * time.Second)
	if _, err := service.Consume(location.ID, "/project"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired consume error = %v", err)
	}
}
