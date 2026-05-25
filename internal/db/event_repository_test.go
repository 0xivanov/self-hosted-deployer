package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestEventRepositoryListsNewestFirstAndCombinesFilters(t *testing.T) {
	ctx := context.Background()
	repo := NewEventRepository(openEventTestDB(t))
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	events := []domain.Event{
		{ID: "event-1", CreatedAt: now, Type: domain.EventTypeNodeOnline, Severity: domain.EventSeverityInfo, Message: "node online", NodeID: "node-1"},
		{ID: "event-2", CreatedAt: now.Add(time.Minute), Type: domain.EventTypeAppDeployFailed, Severity: domain.EventSeverityError, Message: "deploy failed", AppID: "app-1", DeploymentID: "deploy-1"},
		{ID: "event-3", CreatedAt: now.Add(2 * time.Minute), Type: domain.EventTypeAppDeploySucceeded, Severity: domain.EventSeverityInfo, Message: "deploy ready", AppID: "app-1", DeploymentID: "deploy-2"},
	}
	for _, event := range events {
		if err := repo.Create(ctx, event); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	got, err := repo.List(ctx, domain.EventFilter{AppID: "app-1"})
	if err != nil || len(got) != 2 || got[0].ID != "event-3" || got[1].ID != "event-2" {
		t.Fatalf("unexpected app-filtered events %#v: %v", got, err)
	}
	since := now.Add(30 * time.Second)
	got, err = repo.List(ctx, domain.EventFilter{
		AppID:    "app-1",
		Severity: domain.EventSeverityError,
		Since:    &since,
	})
	if err != nil || len(got) != 1 || got[0].ID != "event-2" {
		t.Fatalf("unexpected combined filters %#v: %v", got, err)
	}
	got, err = repo.List(ctx, domain.EventFilter{NodeID: "node-1", Type: domain.EventTypeNodeOnline})
	if err != nil || len(got) != 1 || got[0].ID != "event-1" {
		t.Fatalf("unexpected node/type filter %#v: %v", got, err)
	}
}

func TestEventRepositoryPrunesAgeAndCount(t *testing.T) {
	ctx := context.Background()
	repo := NewEventRepository(openEventTestDB(t))
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	for i, createdAt := range []time.Time{now.Add(-48 * time.Hour), now.Add(-time.Hour), now} {
		if err := repo.Create(ctx, domain.Event{ID: "event-" + string(rune('a'+i)), CreatedAt: createdAt, Type: domain.EventTypeNodeOnline, Severity: domain.EventSeverityInfo, Message: "node online"}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}
	if removed, err := repo.PruneBefore(ctx, now.Add(-24*time.Hour)); err != nil || removed != 1 {
		t.Fatalf("prune before removed=%d err=%v", removed, err)
	}
	if removed, err := repo.PruneToLimit(ctx, 1); err != nil || removed != 1 {
		t.Fatalf("prune count removed=%d err=%v", removed, err)
	}
	got, err := repo.List(ctx, domain.EventFilter{})
	if err != nil || len(got) != 1 || got[0].CreatedAt != now {
		t.Fatalf("unexpected remaining event %#v: %v", got, err)
	}
}

func TestEventRepositoryOrdersFixedWidthTimestampsAndPagesForward(t *testing.T) {
	ctx := context.Background()
	repo := NewEventRepository(openEventTestDB(t))
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	for _, event := range []domain.Event{
		{ID: "event-zero", CreatedAt: now, Type: domain.EventTypeNodeOnline, Severity: domain.EventSeverityInfo, Message: "zero"},
		{ID: "event-nano", CreatedAt: now.Add(time.Nanosecond), Type: domain.EventTypeNodeOnline, Severity: domain.EventSeverityInfo, Message: "nano"},
		{ID: "event-two", CreatedAt: now.Add(2 * time.Nanosecond), Type: domain.EventTypeNodeOnline, Severity: domain.EventSeverityInfo, Message: "two"},
	} {
		if err := repo.Create(ctx, event); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	got, err := repo.List(ctx, domain.EventFilter{})
	if err != nil || len(got) != 3 || got[0].ID != "event-two" || got[1].ID != "event-nano" || got[2].ID != "event-zero" {
		t.Fatalf("unexpected fixed-width ordering %#v: %v", got, err)
	}
	cursor := domain.EventCursor{CreatedAt: now, ID: "event-zero"}
	got, err = repo.List(ctx, domain.EventFilter{After: &cursor, OldestFirst: true})
	if err != nil || len(got) != 2 || got[0].ID != "event-nano" || got[1].ID != "event-two" {
		t.Fatalf("unexpected cursor page %#v: %v", got, err)
	}
	since := now.Add(time.Nanosecond)
	got, err = repo.List(ctx, domain.EventFilter{Since: &since})
	if err != nil || len(got) != 2 || got[0].ID != "event-two" || got[1].ID != "event-nano" {
		t.Fatalf("unexpected fixed-width since results %#v: %v", got, err)
	}
	if removed, err := repo.PruneBefore(ctx, since); err != nil || removed != 1 {
		t.Fatalf("unexpected fixed-width prune result removed=%d err=%v", removed, err)
	}
}

func openEventTestDB(t *testing.T) *Db {
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
