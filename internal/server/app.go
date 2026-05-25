package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	deploymentStatusPending = "pending"
	deploymentStatusFailed  = "failed"
	routeStatusPending      = "pending"
)

type AppRepository interface {
	Create(ctx context.Context, app domain.App) error
	Update(ctx context.Context, app domain.App) error
	FindByName(ctx context.Context, name string) (domain.App, error)
	FindByID(ctx context.Context, appID string) (domain.App, error)
	FindActiveByName(ctx context.Context, name string) (domain.App, error)
	List(ctx context.Context) ([]domain.App, error)
	MarkDeleted(ctx context.Context, name string, deletedAt time.Time) (domain.App, error)
}

type DeploymentRepository interface {
	Create(ctx context.Context, deployment domain.Deployment) error
	UpdateStatus(ctx context.Context, deploymentID string, status string, failureReason string, updatedAt time.Time) error
	ListByApp(ctx context.Context, appID string) ([]domain.Deployment, error)
}

type RouteRepository interface {
	UpsertForApp(ctx context.Context, route domain.Route) error
	DeleteByApp(ctx context.Context, appID string) error
	FindByDomain(ctx context.Context, domainName string) (domain.Route, error)
	List(ctx context.Context) ([]domain.Route, error)
	ListByApp(ctx context.Context, appID string) ([]domain.Route, error)
	UpdateStatus(ctx context.Context, routeID string, status string, updatedAt time.Time) error
}

type AppRuntime interface {
	Reconcile(ctx context.Context, cfg appconfig.Config) error
	Delete(ctx context.Context, appName string) error
	Status(ctx context.Context, appName string) (string, error)
}

type IngressRuntime = AppRuntime

type AppServiceConfig struct {
	Apps            AppRepository
	Deployments     DeploymentRepository
	Routes          RouteRepository
	Runtime         AppRuntime
	Ingress         IngressRuntime
	RouteTLSEnabled bool
}

type AppService struct {
	deployerv1.UnimplementedAppServiceServer
	apps            AppRepository
	deployments     DeploymentRepository
	routes          RouteRepository
	runtime         AppRuntime
	routeTLSEnabled bool
	now             func() time.Time
}

func NewAppService(cfg AppServiceConfig) AppService {
	runtime := cfg.Runtime
	if runtime == nil {
		runtime = cfg.Ingress
	}
	return AppService{
		apps:            cfg.Apps,
		deployments:     cfg.Deployments,
		routes:          cfg.Routes,
		runtime:         runtime,
		routeTLSEnabled: cfg.RouteTLSEnabled,
		now:             time.Now,
	}
}

func (s AppService) DeployApp(ctx context.Context, req *deployerv1.DeployAppRequest) (*deployerv1.DeployAppResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetDeployerYaml()) == "" {
		return nil, status.Error(codes.InvalidArgument, "deployer_yaml is required")
	}

	cfg, err := appconfig.Parse([]byte(req.GetDeployerYaml()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.validateRequestedDomain(ctx, cfg.Name, cfg.Routing.Domain); err != nil {
		return nil, err
	}
	desiredStateJSON, err := cfg.JSON()
	if err != nil {
		return nil, status.Error(codes.Internal, "encode desired state")
	}

	now := s.now().UTC()
	app, err := s.apps.FindByName(ctx, cfg.Name)
	if errors.Is(err, db.ErrNotFound) {
		appID, err := newID("app")
		if err != nil {
			return nil, status.Error(codes.Internal, "create app id")
		}
		app = domain.App{
			ID:               appID,
			Name:             cfg.Name,
			Image:            cfg.Image,
			DesiredStateJSON: desiredStateJSON,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := s.apps.Create(ctx, app); err != nil {
			return nil, status.Error(codes.Internal, "create app")
		}
	} else if err != nil {
		return nil, status.Error(codes.Internal, "find app")
	} else {
		app.Image = cfg.Image
		app.DesiredStateJSON = desiredStateJSON
		app.UpdatedAt = now
		app.DeletedAt = nil
		if err := s.apps.Update(ctx, app); err != nil {
			return nil, status.Error(codes.Internal, "update app")
		}
	}

	deploymentID, err := newID("deploy")
	if err != nil {
		return nil, status.Error(codes.Internal, "create deployment id")
	}
	deployment := domain.Deployment{
		ID:        deploymentID,
		AppID:     app.ID,
		Status:    deploymentStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.deployments.Create(ctx, deployment); err != nil {
		return nil, status.Error(codes.Internal, "create deployment")
	}
	if s.runtime != nil {
		if err := s.runtime.Reconcile(ctx, cfg); err != nil {
			_ = s.deployments.UpdateStatus(ctx, deployment.ID, deploymentStatusFailed, err.Error(), now)
			return nil, status.Error(codes.Internal, "apply app resources")
		}
	}
	if err := s.syncRoute(ctx, app, cfg, now); err != nil {
		return nil, err
	}

	appProto, err := protoApp(app)
	if err != nil {
		return nil, status.Error(codes.Internal, "decode desired state")
	}
	return &deployerv1.DeployAppResponse{
		App:        appProto,
		Deployment: protoDeployment(deployment),
	}, nil
}

func (s AppService) ListApps(ctx context.Context, _ *deployerv1.ListAppsRequest) (*deployerv1.ListAppsResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	apps, err := s.apps.List(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "list apps")
	}
	response := &deployerv1.ListAppsResponse{
		Apps: make([]*deployerv1.App, 0, len(apps)),
	}
	for _, app := range apps {
		appProto, err := protoApp(app)
		if err != nil {
			return nil, status.Error(codes.Internal, "decode desired state")
		}
		response.Apps = append(response.Apps, appProto)
	}
	return response, nil
}

func (s AppService) InspectApp(ctx context.Context, req *deployerv1.InspectAppRequest) (*deployerv1.InspectAppResponse, error) {
	app, deployments, routes, err := s.inspectApp(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	appProto, err := protoApp(app)
	if err != nil {
		return nil, status.Error(codes.Internal, "decode desired state")
	}
	response := &deployerv1.InspectAppResponse{
		App:         appProto,
		Deployments: make([]*deployerv1.Deployment, 0, len(deployments)),
	}
	for _, deployment := range deployments {
		response.Deployments = append(response.Deployments, protoDeployment(deployment))
	}
	for _, route := range routes {
		response.Routes = append(response.Routes, protoRoute(route))
	}
	return response, nil
}

func (s AppService) DeleteApp(ctx context.Context, req *deployerv1.DeleteAppRequest) (*deployerv1.DeleteAppResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	app, err := s.apps.FindActiveByName(ctx, name)
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "app not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "get app")
	}
	if s.runtime != nil {
		if err := s.runtime.Delete(ctx, app.Name); err != nil {
			return nil, status.Error(codes.Internal, "delete app resources")
		}
	}
	app, err = s.apps.MarkDeleted(ctx, name, s.now().UTC())
	if err != nil {
		return nil, status.Error(codes.Internal, "delete app")
	}
	if s.routes != nil {
		if err := s.routes.DeleteByApp(ctx, app.ID); err != nil {
			return nil, status.Error(codes.Internal, "delete app routes")
		}
	}
	appProto, err := protoApp(app)
	if err != nil {
		return nil, status.Error(codes.Internal, "decode desired state")
	}
	return &deployerv1.DeleteAppResponse{App: appProto}, nil
}

func (s AppService) GetApp(ctx context.Context, req *deployerv1.GetAppRequest) (*deployerv1.GetAppResponse, error) {
	app, deployments, routes, err := s.inspectApp(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	appProto, err := protoApp(app)
	if err != nil {
		return nil, status.Error(codes.Internal, "decode desired state")
	}
	response := &deployerv1.GetAppResponse{
		App:         appProto,
		Deployments: make([]*deployerv1.Deployment, 0, len(deployments)),
	}
	for _, deployment := range deployments {
		response.Deployments = append(response.Deployments, protoDeployment(deployment))
	}
	for _, route := range routes {
		response.Routes = append(response.Routes, protoRoute(route))
	}
	return response, nil
}

func (s AppService) GetAppStatus(ctx context.Context, req *deployerv1.GetAppStatusRequest) (*deployerv1.GetAppStatusResponse, error) {
	app, deployments, routes, err := s.inspectApp(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	appProto, err := protoApp(app)
	if err != nil {
		return nil, status.Error(codes.Internal, "decode desired state")
	}
	response := &deployerv1.GetAppStatusResponse{App: appProto}
	if len(deployments) > 0 {
		response.LatestDeployment = protoDeployment(deployments[0])
	}
	for _, route := range routes {
		response.Routes = append(response.Routes, protoRoute(route))
	}
	return response, nil
}

func (s AppService) ListRoutes(ctx context.Context, _ *deployerv1.ListRoutesRequest) (*deployerv1.ListRoutesResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if s.routes == nil {
		return &deployerv1.ListRoutesResponse{Routes: []*deployerv1.Route{}}, nil
	}
	routes, err := s.routes.List(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "list routes")
	}
	response := &deployerv1.ListRoutesResponse{
		Routes: make([]*deployerv1.Route, 0, len(routes)),
	}
	for _, route := range routes {
		app, err := s.apps.FindByID(ctx, route.AppID)
		if err != nil {
			return nil, status.Error(codes.Internal, "get route app")
		}
		route, err = s.refreshRouteStatus(ctx, app, route)
		if err != nil {
			return nil, err
		}
		response.Routes = append(response.Routes, protoRoute(route))
	}
	return response, nil
}

func (s AppService) InspectRoute(ctx context.Context, req *deployerv1.InspectRouteRequest) (*deployerv1.InspectRouteResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	domainName := strings.TrimSpace(req.GetDomain())
	if domainName == "" {
		return nil, status.Error(codes.InvalidArgument, "domain is required")
	}
	if s.routes == nil {
		return nil, status.Error(codes.NotFound, "route not found")
	}
	route, err := s.routes.FindByDomain(ctx, domainName)
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "route not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "get route")
	}
	app, err := s.apps.FindByID(ctx, route.AppID)
	if err != nil {
		return nil, status.Error(codes.Internal, "get route app")
	}
	route, err = s.refreshRouteStatus(ctx, app, route)
	if err != nil {
		return nil, err
	}
	return &deployerv1.InspectRouteResponse{Route: protoRoute(route)}, nil
}

func (s AppService) inspectApp(ctx context.Context, name string) (domain.App, []domain.Deployment, []domain.Route, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return domain.App{}, nil, nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.App{}, nil, nil, status.Error(codes.InvalidArgument, "name is required")
	}
	app, err := s.apps.FindActiveByName(ctx, name)
	if errors.Is(err, db.ErrNotFound) {
		return domain.App{}, nil, nil, status.Error(codes.NotFound, "app not found")
	}
	if err != nil {
		return domain.App{}, nil, nil, status.Error(codes.Internal, "get app")
	}
	deployments, err := s.deployments.ListByApp(ctx, app.ID)
	if err != nil {
		return domain.App{}, nil, nil, status.Error(codes.Internal, "list app deployments")
	}
	routes := []domain.Route{}
	if s.routes != nil {
		routes, err = s.routes.ListByApp(ctx, app.ID)
		if err != nil {
			return domain.App{}, nil, nil, status.Error(codes.Internal, "list app routes")
		}
		for i, route := range routes {
			route, err = s.refreshRouteStatus(ctx, app, route)
			if err != nil {
				return domain.App{}, nil, nil, err
			}
			routes[i] = route
		}
	}
	return app, deployments, routes, nil
}

func (s AppService) syncRoute(ctx context.Context, app domain.App, cfg appconfig.Config, now time.Time) error {
	if s.routes == nil {
		return nil
	}
	domainName := strings.TrimSpace(cfg.Routing.Domain)
	if domainName == "" {
		if err := s.routes.DeleteByApp(ctx, app.ID); err != nil {
			return status.Error(codes.Internal, "delete app route")
		}
		return nil
	}
	routeID, err := newID("route")
	if err != nil {
		return status.Error(codes.Internal, "create route id")
	}
	if err := s.routes.UpsertForApp(ctx, domain.Route{
		ID:         routeID,
		AppID:      app.ID,
		Domain:     domainName,
		TargetPort: cfg.Service.Port,
		Status:     routeStatusPending,
		TLSEnabled: s.routeTLSEnabled,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		return status.Error(codes.Internal, "sync app route")
	}
	return nil
}

func (s AppService) validateRequestedDomain(ctx context.Context, appName string, domainName string) error {
	if s.routes == nil || strings.TrimSpace(domainName) == "" {
		return nil
	}
	route, err := s.routes.FindByDomain(ctx, strings.TrimSpace(domainName))
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return status.Error(codes.Internal, "check route domain")
	}
	app, err := s.apps.FindByID(ctx, route.AppID)
	if err != nil {
		return status.Error(codes.Internal, "get route app")
	}
	if app.Name != appName {
		return status.Error(codes.AlreadyExists, "domain is already routed")
	}
	return nil
}

func (s AppService) refreshRouteStatus(ctx context.Context, app domain.App, route domain.Route) (domain.Route, error) {
	if s.runtime == nil {
		return route, nil
	}
	runtimeStatus, err := s.runtime.Status(ctx, app.Name)
	if err != nil {
		return domain.Route{}, status.Error(codes.Internal, "read route status")
	}
	if runtimeStatus == route.Status {
		return route, nil
	}
	updatedAt := s.now().UTC()
	if err := s.routes.UpdateStatus(ctx, route.ID, runtimeStatus, updatedAt); err != nil {
		return domain.Route{}, status.Error(codes.Internal, "update route status")
	}
	route.Status = runtimeStatus
	route.UpdatedAt = updatedAt
	return route, nil
}

func protoApp(app domain.App) (*deployerv1.App, error) {
	cfg, err := appconfig.FromJSON(app.DesiredStateJSON)
	if err != nil {
		return nil, fmt.Errorf("decode desired state for app %q: %w", app.Name, err)
	}
	return &deployerv1.App{
		Id:           app.ID,
		Name:         app.Name,
		Image:        app.Image,
		Replicas:     int32(cfg.Deploy.Replicas),
		DesiredState: app.DesiredStateJSON,
		CreatedAt:    formatProtoTime(app.CreatedAt),
		UpdatedAt:    formatProtoTime(app.UpdatedAt),
	}, nil
}

func protoRoute(route domain.Route) *deployerv1.Route {
	return &deployerv1.Route{
		Id:         route.ID,
		AppId:      route.AppID,
		Domain:     route.Domain,
		TargetPort: int32(route.TargetPort),
		Status:     route.Status,
		TlsEnabled: route.TLSEnabled,
	}
}

func protoDeployment(deployment domain.Deployment) *deployerv1.Deployment {
	return &deployerv1.Deployment{
		Id:            deployment.ID,
		AppId:         deployment.AppID,
		Status:        deployment.Status,
		FailureReason: deployment.FailureReason,
		CreatedAt:     formatProtoTime(deployment.CreatedAt),
		UpdatedAt:     formatProtoTime(deployment.UpdatedAt),
	}
}
