package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestRouteRepositoryCreateUpsertListAndDelete(t *testing.T) {
	ctx := context.Background()
	database := openRepositoryTestDB(t)
	repo := NewRouteRepository(database)
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, domain.Route{
		ID:         "route-1",
		AppID:      "app-1",
		Domain:     "api.example.com",
		TargetPort: 3000,
		Status:     "pending",
		TLSEnabled: false,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	route, err := repo.FindByDomain(ctx, "api.example.com")
	if err != nil {
		t.Fatalf("find route: %v", err)
	}
	if route.AppID != "app-1" || route.TargetPort != 3000 || route.Status != "pending" || route.TLSEnabled {
		t.Fatalf("unexpected route: %#v", route)
	}

	updatedAt := now.Add(time.Minute)
	if err := repo.UpsertForApp(ctx, domain.Route{
		ID:         "route-new-id-ignored",
		AppID:      "app-1",
		Domain:     "new.example.com",
		TargetPort: 8080,
		Status:     "healthy",
		TLSEnabled: true,
		CreatedAt:  updatedAt,
		UpdatedAt:  updatedAt,
	}); err != nil {
		t.Fatalf("upsert route: %v", err)
	}

	routes, err := repo.ListByApp(ctx, "app-1")
	if err != nil {
		t.Fatalf("list by app: %v", err)
	}
	if len(routes) != 1 || routes[0].ID != "route-1" || routes[0].Domain != "new.example.com" || routes[0].TargetPort != 8080 || routes[0].Status != "healthy" || !routes[0].TLSEnabled {
		t.Fatalf("unexpected upsert result: %#v", routes)
	}

	listed, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(listed) != 1 || listed[0].Domain != "new.example.com" {
		t.Fatalf("unexpected route list: %#v", listed)
	}

	if err := repo.UpdateStatus(ctx, "route-1", "unavailable", updatedAt.Add(time.Minute)); err != nil {
		t.Fatalf("update route status: %v", err)
	}
	route, err = repo.FindByDomain(ctx, "new.example.com")
	if err != nil || route.Status != "unavailable" {
		t.Fatalf("expected updated route status, got %#v: %v", route, err)
	}

	if err := repo.DeleteByApp(ctx, "app-1"); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if _, err := repo.FindByDomain(ctx, "new.example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted route to be hidden, got %v", err)
	}
}
