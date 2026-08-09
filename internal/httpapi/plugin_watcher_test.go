package httpapi

import (
	"testing"
	"time"
)

func TestResetPluginWatchTimerResetsActiveTimer(t *testing.T) {
	timer := resetPluginWatchTimer(nil, time.Hour)
	defer timer.Stop()

	timer = resetPluginWatchTimer(timer, time.Millisecond)
	select {
	case <-timer.C:
	case <-time.After(time.Second):
		t.Fatal("reset active timer did not fire")
	}
}
