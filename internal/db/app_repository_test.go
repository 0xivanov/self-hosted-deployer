package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestAppRepositoryCreateUpdateGetListAndDelete(t *testing.T) {
	ctx := context.Background()
	repo := NewAppRepository(openRepositoryTestDB(t))
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	app := domain.App{
		ID:               "app-1",
		Name:             "my-api",
		Image:            "ivan/my-api:1.0.0",
		DesiredStateJSON: `{"name":"my-api","image":"ivan/my-api:1.0.0"}`,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := repo.Create(ctx, app); err != nil {
		t.Fatalf("create app: %v", err)
	}
	if err := repo.Create(ctx, app); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected duplicate app name error, got %v", err)
	}

	app.Image = "ivan/my-api:1.0.1"
	app.DesiredStateJSON = `{"name":"my-api","image":"ivan/my-api:1.0.1"}`
	app.UpdatedAt = now.Add(time.Minute)
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update app: %v", err)
	}

	updated, err := repo.FindActiveByName(ctx, "my-api")
	if err != nil {
		t.Fatalf("find active app: %v", err)
	}
	if updated.Image != "ivan/my-api:1.0.1" || !updated.UpdatedAt.Equal(app.UpdatedAt) {
		t.Fatalf("app update was not stored: %#v", updated)
	}

	apps, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list apps: %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "my-api" {
		t.Fatalf("unexpected app list: %#v", apps)
	}

	deleted, err := repo.MarkDeleted(ctx, "my-api", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	if deleted.DeletedAt == nil {
		t.Fatalf("expected deleted_at to be set: %#v", deleted)
	}
	if _, err := repo.FindActiveByName(ctx, "my-api"); err != ErrNotFound {
		t.Fatalf("deleted app should not be active, got %v", err)
	}
}
