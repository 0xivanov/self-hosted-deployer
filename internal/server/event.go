package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)

type EventRepository interface {
	Create(ctx context.Context, event domain.Event) error
	List(ctx context.Context, filter domain.EventFilter) ([]domain.Event, error)
	PruneBefore(ctx context.Context, cutoff time.Time) (int64, error)
	PruneToLimit(ctx context.Context, maxCount int) (int64, error)
}

type EventRecorder interface {
	Record(ctx context.Context, event domain.Event) error
}

type RepositoryEventRecorder struct {
	events EventRepository
	logger *slog.Logger
	now    func() time.Time
}

func NewEventRecorder(events EventRepository, logger *slog.Logger) *RepositoryEventRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &RepositoryEventRecorder{events: events, logger: logger, now: time.Now}
}

func (r *RepositoryEventRecorder) Record(ctx context.Context, event domain.Event) error {
	if err := validateEvent(event); err != nil {
		return err
	}
	if event.ID == "" {
		id, err := newID("event")
		if err != nil {
			return err
		}
		event.ID = id
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = r.now().UTC()
	}
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	if r.events == nil {
		return errors.New("event repository is not configured")
	}
	if err := r.events.Create(ctx, event); err != nil {
		r.logger.WarnContext(ctx, "record platform event", "type", event.Type, "error", err)
		return err
	}
	return nil
}

func validateEvent(event domain.Event) error {
	if !eventTypePattern.MatchString(string(event.Type)) {
		return fmt.Errorf("invalid event type %q", event.Type)
	}
	switch event.Severity {
	case domain.EventSeverityInfo, domain.EventSeverityWarning, domain.EventSeverityError:
	default:
		return fmt.Errorf("invalid event severity %q", event.Severity)
	}
	if strings.TrimSpace(event.Message) == "" {
		return errors.New("event message is required")
	}
	if event.MetadataJSON != "" && !json.Valid([]byte(event.MetadataJSON)) {
		return errors.New("event metadata_json must be valid JSON")
	}
	return nil
}

func metadataJSON(values map[string]any) string {
	data, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func recordEvent(ctx context.Context, recorder EventRecorder, event domain.Event) {
	if recorder != nil {
		_ = recorder.Record(ctx, event)
	}
}

type EventServiceConfig struct {
	Events       EventRepository
	Apps         AppRepository
	Nodes        NodeRepository
	PollInterval time.Duration
	Now          func() time.Time
}

type EventService struct {
	deployerv1.UnimplementedEventServiceServer
	events       EventRepository
	apps         AppRepository
	nodes        NodeRepository
	pollInterval time.Duration
	now          func() time.Time
}

func NewEventService(cfg EventServiceConfig) EventService {
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return EventService{
		events:       cfg.Events,
		apps:         cfg.Apps,
		nodes:        cfg.Nodes,
		pollInterval: pollInterval,
		now:          now,
	}
}

func (s EventService) ListEvents(ctx context.Context, req *deployerv1.ListEventsRequest) (*deployerv1.ListEventsResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	filter, err := s.filter(ctx, req.GetApp(), req.GetNode(), req.GetType(), req.GetSeverity(), req.GetSince(), req.GetLimit())
	if err != nil {
		return nil, err
	}
	events, err := s.events.List(ctx, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, "list events")
	}
	response := &deployerv1.ListEventsResponse{Events: make([]*deployerv1.Event, 0, len(events))}
	for _, event := range events {
		response.Events = append(response.Events, protoEvent(event))
	}
	return response, nil
}

func (s EventService) WatchEvents(req *deployerv1.WatchEventsRequest, stream deployerv1.EventService_WatchEventsServer) error {
	ctx := stream.Context()
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return err
	}
	filter, err := s.filter(ctx, req.GetApp(), req.GetNode(), req.GetType(), req.GetSeverity(), req.GetSince(), 1000)
	if err != nil {
		return err
	}
	if filter.Since == nil {
		since := s.now().UTC()
		filter.Since = &since
	}

	seen := map[string]struct{}{}
	sendNew := func() error {
		events, err := s.events.List(ctx, filter)
		if err != nil {
			return status.Error(codes.Internal, "watch events")
		}
		for i := len(events) - 1; i >= 0; i-- {
			event := events[i]
			if _, ok := seen[event.ID]; ok {
				continue
			}
			if err := stream.Send(&deployerv1.WatchEventsResponse{Event: protoEvent(event)}); err != nil {
				return err
			}
			seen[event.ID] = struct{}{}
		}
		return nil
	}
	if err := sendNew(); err != nil {
		return err
	}
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := sendNew(); err != nil {
				return err
			}
		}
	}
}

func (s EventService) filter(ctx context.Context, appRef string, nodeRef string, eventType string, severity string, since string, limit int32) (domain.EventFilter, error) {
	filter := domain.EventFilter{Limit: int(limit)}
	appRef = strings.TrimSpace(appRef)
	if appRef != "" {
		app, err := s.apps.FindByName(ctx, appRef)
		if errors.Is(err, db.ErrNotFound) {
			app, err = s.apps.FindByID(ctx, appRef)
		}
		if errors.Is(err, db.ErrNotFound) {
			return filter, status.Error(codes.NotFound, "app not found")
		}
		if err != nil {
			return filter, status.Error(codes.Internal, "get event app")
		}
		filter.AppID = app.ID
	}
	nodeRef = strings.TrimSpace(nodeRef)
	if nodeRef != "" {
		node, err := s.nodes.FindByName(ctx, nodeRef)
		if errors.Is(err, db.ErrNotFound) {
			node, err = s.nodes.FindByID(ctx, nodeRef)
		}
		if errors.Is(err, db.ErrNotFound) {
			return filter, status.Error(codes.NotFound, "node not found")
		}
		if err != nil {
			return filter, status.Error(codes.Internal, "get event node")
		}
		filter.NodeID = node.ID
	}
	eventType = strings.TrimSpace(eventType)
	if eventType != "" {
		if !eventTypePattern.MatchString(eventType) {
			return filter, status.Error(codes.InvalidArgument, "event type must be a dot-separated name")
		}
		filter.Type = domain.EventType(eventType)
	}
	severity = strings.TrimSpace(severity)
	if severity != "" {
		filter.Severity = domain.EventSeverity(severity)
		switch filter.Severity {
		case domain.EventSeverityInfo, domain.EventSeverityWarning, domain.EventSeverityError:
		default:
			return filter, status.Error(codes.InvalidArgument, "severity must be one of info, warning, error")
		}
	}
	since = strings.TrimSpace(since)
	if since != "" {
		parsed, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			return filter, status.Error(codes.InvalidArgument, "since must be an RFC3339 timestamp")
		}
		parsed = parsed.UTC()
		filter.Since = &parsed
	}
	return filter, nil
}

func protoEvent(event domain.Event) *deployerv1.Event {
	return &deployerv1.Event{
		Id:           event.ID,
		CreatedAt:    formatProtoTime(event.CreatedAt),
		Type:         string(event.Type),
		Severity:     string(event.Severity),
		Message:      event.Message,
		AppId:        event.AppID,
		NodeId:       event.NodeID,
		DeploymentId: event.DeploymentID,
		MetadataJson: event.MetadataJSON,
	}
}

func RunEventRetention(ctx context.Context, events EventRepository, cfg config.EventRetentionConfig, logger *slog.Logger) {
	if events == nil {
		return
	}
	prune := func() {
		if _, err := events.PruneBefore(ctx, time.Now().UTC().Add(-cfg.MaxAge)); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "prune events by age", "error", err)
		}
		if _, err := events.PruneToLimit(ctx, cfg.MaxCount); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "prune events by count", "error", err)
		}
	}
	prune()
	ticker := time.NewTicker(cfg.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
