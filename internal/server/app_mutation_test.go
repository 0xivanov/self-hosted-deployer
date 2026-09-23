package server

import (
	"context"
	"testing"
	"time"
)

func TestAppMutationLocksAllowOtherAppsAndForgetIdleLocks(t *testing.T) {
	service := NewAppService(AppServiceConfig{})
	release, err := service.acquireOperation(t.Context(), "one")
	if err != nil {
		t.Fatal(err)
	}
	// Copying an AppService must share its mutation locks.
	copyService := service
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := copyService.acquireOperation(ctx, "one"); err == nil {
		t.Fatal("same app mutated concurrently")
	}
	other, err := copyService.acquireOperation(t.Context(), "two")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	service.operationMu.mu.Lock()
	defer service.operationMu.mu.Unlock()
	if len(service.operationMu.apps) != 0 {
		t.Fatal("idle lock retained")
	}
}
