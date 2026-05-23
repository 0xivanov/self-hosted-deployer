package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestNodeRepositoryCreateListFindAndUpdate(t *testing.T) {
	ctx := context.Background()
	database := openRepositoryTestDB(t)
	repo := NewNodeRepository(database)
	now := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, domain.Node{
		ID:         "node-1",
		Name:       "pi-kitchen",
		LabelsJSON: `{"location":"home","role":"worker"}`,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}

	byID, err := repo.FindByID(ctx, "node-1")
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if byID.Status != "pending" {
		t.Fatalf("expected default pending status, got %q", byID.Status)
	}

	byName, err := repo.FindByName(ctx, "pi-kitchen")
	if err != nil {
		t.Fatalf("find by name: %v", err)
	}
	if byName.ID != "node-1" {
		t.Fatalf("expected node-1, got %q", byName.ID)
	}

	nodes, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "pi-kitchen" {
		t.Fatalf("unexpected node list: %#v", nodes)
	}

	seenAt := now.Add(time.Minute)
	if err := repo.UpdateHeartbeat(ctx, "node-1", domain.Node{
		Status:   "online",
		Hostname: "pi-kitchen.local",
		Arch:     "linux/arm64",
		OS:       "linux",
		Kernel:   "6.6",
	}, seenAt); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	updated, err := repo.FindByID(ctx, "node-1")
	if err != nil {
		t.Fatalf("find updated node: %v", err)
	}
	if updated.Status != "online" || updated.LastSeenAt == nil || !updated.LastSeenAt.Equal(seenAt) {
		t.Fatalf("heartbeat fields were not updated: %#v", updated)
	}
	if updated.Hostname != "pi-kitchen.local" || updated.Arch != "linux/arm64" || updated.OS != "linux" || updated.Kernel != "6.6" {
		t.Fatalf("metadata fields were not updated: %#v", updated)
	}

	if err := repo.UpdateStatus(ctx, "node-1", "drained", seenAt.Add(time.Minute)); err != nil {
		t.Fatalf("update status: %v", err)
	}
	drained, err := repo.FindByID(ctx, "node-1")
	if err != nil {
		t.Fatalf("find drained node: %v", err)
	}
	if drained.Status != "drained" {
		t.Fatalf("expected drained status, got %q", drained.Status)
	}
}

func openRepositoryTestDB(t *testing.T) *Db {
	t.Helper()
	database, err := Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "deployer.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	return database
}
