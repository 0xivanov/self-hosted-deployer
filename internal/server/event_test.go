package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEventRecorderFillsIdentityTimestampAndDoesNotLogMetadata(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repository := db.NewEventRepository(database)
	recorder := NewEventRecorder(repository, nil)
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	recorder.now = func() time.Time { return now }

	if err := recorder.Record(ctx, domain.Event{
		Type: domain.EventTypeSecretCreated, Severity: domain.EventSeverityInfo, Message: "secret created",
		MetadataJSON: `{"secret_name":"DATABASE_URL"}`,
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}
	events, err := repository.List(ctx, domain.EventFilter{})
	if err != nil || len(events) != 1 || events[0].ID == "" || !events[0].CreatedAt.Equal(now) {
		t.Fatalf("unexpected recorded event %#v: %v", events, err)
	}

	var logOutput bytes.Buffer
	failing := NewEventRecorder(failingEventRepository{}, slog.New(slog.NewJSONHandler(&logOutput, nil)))
	err = failing.Record(ctx, domain.Event{
		Type: domain.EventTypeSecretUpdated, Severity: domain.EventSeverityInfo, Message: "secret updated",
		MetadataJSON: `{"value":"do-not-log"}`,
	})
	if err == nil || strings.Contains(logOutput.String(), "do-not-log") {
		t.Fatalf("expected storage failure without metadata leakage, err=%v log=%q", err, logOutput.String())
	}
}

func TestEventServiceListsFilteredEventsAndWatchesExistingWindow(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	createSecretTestApp(t, apps, nil)
	events := db.NewEventRepository(database)
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	if err := events.Create(ctx, domain.Event{
		ID: "event-1", CreatedAt: now, Type: domain.EventTypeAppDeployFailed, Severity: domain.EventSeverityError,
		Message: "deployment failed", AppID: "app-1",
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	service := NewEventService(EventServiceConfig{Events: events, Apps: apps, Nodes: db.NewNodeRepository(database), PollInterval: time.Millisecond})
	response, err := service.ListEvents(ctx, &deployerv1.ListEventsRequest{App: "my-api", Severity: "error"})
	if err != nil || len(response.GetEvents()) != 1 || response.GetEvents()[0].GetType() != string(domain.EventTypeAppDeployFailed) {
		t.Fatalf("unexpected events response %#v: %v", response, err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &recordingEventStream{ctx: streamCtx, cancel: cancel, stopAfter: 1}
	err = service.WatchEvents(&deployerv1.WatchEventsRequest{Since: now.Add(-time.Second).Format(time.RFC3339Nano)}, stream)
	if !errors.Is(err, context.Canceled) || len(stream.events) != 1 || stream.events[0].GetId() != "event-1" {
		t.Fatalf("unexpected watch results %#v: %v", stream.events, err)
	}
}

func TestEventServiceWatchEventsDrainsMoreThanOnePage(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	repository := db.NewEventRepository(openTestDB(t))
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	const total = eventWatchBatchSize + 1
	for i := 0; i < total; i++ {
		if err := repository.Create(ctx, domain.Event{
			ID:        fmt.Sprintf("event-%04d", i),
			CreatedAt: now.Add(time.Duration(i+1) * time.Nanosecond),
			Type:      domain.EventTypeNodeOnline,
			Severity:  domain.EventSeverityInfo,
			Message:   "node online",
		}); err != nil {
			t.Fatalf("create event %d: %v", i, err)
		}
	}
	service := NewEventService(EventServiceConfig{Events: repository, PollInterval: time.Millisecond})
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &recordingEventStream{ctx: streamCtx, cancel: cancel, stopAfter: total}

	err := service.WatchEvents(&deployerv1.WatchEventsRequest{Since: now.Format(time.RFC3339Nano)}, stream)
	if !errors.Is(err, context.Canceled) || len(stream.events) != total {
		t.Fatalf("unexpected paged watch results count=%d err=%v", len(stream.events), err)
	}
	if stream.events[0].GetId() != "event-0000" || stream.events[total-1].GetId() != "event-1000" {
		t.Fatalf("unexpected paged watch order first=%q last=%q", stream.events[0].GetId(), stream.events[total-1].GetId())
	}
}

func TestEventServiceRequiresAdminCaller(t *testing.T) {
	service := NewEventService(EventServiceConfig{})
	if _, err := service.ListEvents(context.Background(), &deployerv1.ListEventsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated event list rejection, got %v", err)
	}
}

type recordingEventRecorder struct {
	events []domain.Event
	err    error
}

func (r *recordingEventRecorder) Record(_ context.Context, event domain.Event) error {
	r.events = append(r.events, event)
	return r.err
}

func (r *recordingEventRecorder) hasType(eventType domain.EventType) bool {
	for _, event := range r.events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

type failingEventRepository struct{}

func (failingEventRepository) Create(context.Context, domain.Event) error {
	return errors.New("write failed")
}

func (failingEventRepository) List(context.Context, domain.EventFilter) ([]domain.Event, error) {
	return nil, nil
}

func (failingEventRepository) PruneBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (failingEventRepository) PruneToLimit(context.Context, int) (int64, error) {
	return 0, nil
}

type recordingEventStream struct {
	grpc.ServerStream
	ctx       context.Context
	cancel    context.CancelFunc
	events    []*deployerv1.Event
	stopAfter int
}

func (s *recordingEventStream) Context() context.Context {
	return s.ctx
}

func (s *recordingEventStream) Send(response *deployerv1.WatchEventsResponse) error {
	s.events = append(s.events, response.GetEvent())
	if s.stopAfter > 0 && len(s.events) >= s.stopAfter {
		s.cancel()
	}
	return nil
}
