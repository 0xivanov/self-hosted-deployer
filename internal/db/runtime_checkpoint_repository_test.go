package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func seedRuntimeCheckpointRequest(t *testing.T, database *Db, requestID string) {
	t.Helper()
	now := time.Now().UTC()
	if _, created, err := NewDeploymentRequestRepository(database).Begin(context.Background(), domain.DeployRequest{
		AppName: "my-api", RequestID: requestID, RequestedState: `{"name":"my-api"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil || !created {
		t.Fatalf("seed request: created=%v err=%v", created, err)
	}
}

func TestRuntimeCheckpointPersistsIntentAndCASGates(t *testing.T) {
	database := openRepositoryTestDB(t)
	requestID := strings.Repeat("a", 64)
	seedRuntimeCheckpointRequest(t, database, requestID)
	repo := NewRuntimeCheckpointRepository(database)
	intent := `{"app":"my-api","previous":"old"}`
	if err := repo.SaveActivationIntent(context.Background(), "my-api", requestID, intent); err != nil {
		t.Fatalf("save intent: %v", err)
	}
	if err := repo.SaveActivationIntent(context.Background(), "my-api", requestID, intent); err != nil {
		t.Fatalf("idempotent intent retry: %v", err)
	}
	if !errors.Is(repo.SaveActivationIntent(context.Background(), "my-api", requestID, `{"app":"changed"}`), ErrRuntimeCheckpointConflict) {
		t.Fatal("changed intent was accepted")
	}
	if err := repo.RecordActivationGate(context.Background(), "my-api", requestID, "prepared", "fenced", `{"token":"fence"}`); err != nil {
		t.Fatalf("prepared to fenced: %v", err)
	}
	if !errors.Is(repo.RecordActivationGate(context.Background(), "my-api", requestID, "prepared", "fenced", `{}`), ErrRuntimeCheckpointConflict) {
		t.Fatal("stale gate CAS was accepted")
	}
	if err := repo.RecordActivationGate(context.Background(), "my-api", requestID, "fenced", "activated", `{"token":"active"}`); err != nil {
		t.Fatalf("fenced to activated: %v", err)
	}
	checkpoint, err := repo.FindRuntimeCheckpoint(context.Background(), "my-api", requestID)
	if err != nil || checkpoint.Stage != "activated" || checkpoint.IntentJSON != intent || checkpoint.GateJSON != `{"token":"active"}` {
		t.Fatalf("checkpoint: %#v err=%v", checkpoint, err)
	}
}

func TestRuntimeCheckpointSurvivesReopenAndTerminalFreezes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	database, err := Open(context.Background(), "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("b", 64)
	seedRuntimeCheckpointRequest(t, database, requestID)
	repo := NewRuntimeCheckpointRepository(database)
	if err := repo.SaveActivationIntent(context.Background(), "my-api", requestID, `{"previous":{}}`); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordActivationGate(context.Background(), "my-api", requestID, "prepared", "recovering", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(context.Background(), "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repo = NewRuntimeCheckpointRepository(database)
	if err := repo.RecordActivationGate(context.Background(), "my-api", requestID, "recovering", "withdrawn", `{"restored":true}`); err != nil {
		t.Fatalf("recover to withdrawn: %v", err)
	}
	if err := NewDeploymentRequestRepository(database).Complete(context.Background(), "my-api", requestID, "withdrawn", `{"withdrawal_confirmed":true}`, time.Now().UTC()); err != nil {
		t.Fatalf("complete request: %v", err)
	}
	if !errors.Is(repo.SaveActivationIntent(context.Background(), "my-api", requestID, `{"previous":{}}`), ErrRuntimeCheckpointTerminal) {
		t.Fatal("terminal intent mutation was accepted")
	}
	if !errors.Is(repo.RecordActivationGate(context.Background(), "my-api", requestID, "withdrawn", "activated", `{}`), ErrRuntimeCheckpointTerminal) {
		t.Fatal("invalid terminal transition did not reject")
	}
	checkpoint, err := repo.FindRuntimeCheckpoint(context.Background(), "my-api", requestID)
	if err != nil || checkpoint.Stage != "withdrawn" {
		t.Fatalf("terminal checkpoint: %#v err=%v", checkpoint, err)
	}
}

func TestRuntimeCheckpointRejectsNonObjectOrOversizedJSON(t *testing.T) {
	database := openRepositoryTestDB(t)
	requestID := strings.Repeat("c", 64)
	seedRuntimeCheckpointRequest(t, database, requestID)
	repo := NewRuntimeCheckpointRepository(database)
	for _, intent := range []string{`[]`, `null`, strings.Repeat("x", runtimeCheckpointJSONLimit+1)} {
		if err := repo.SaveActivationIntent(context.Background(), "my-api", requestID, intent); !errors.Is(err, ErrRuntimeCheckpointIdentity) {
			t.Fatalf("intent %q accepted with err=%v", intent[:min(len(intent), 8)], err)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
