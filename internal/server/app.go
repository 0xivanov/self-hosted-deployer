package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const deploymentStatusPending = "pending"

type AppRepository interface {
	Create(ctx context.Context, app domain.App) error
	Update(ctx context.Context, app domain.App) error
	FindByName(ctx context.Context, name string) (domain.App, error)
	FindActiveByName(ctx context.Context, name string) (domain.App, error)
	List(ctx context.Context) ([]domain.App, error)
	MarkDeleted(ctx context.Context, name string, deletedAt time.Time) (domain.App, error)
}

type DeploymentRepository interface {
	Create(ctx context.Context, deployment domain.Deployment) error
	UpdateStatus(ctx context.Context, deploymentID string, status string, failureReason string, updatedAt time.Time) error
	ListByApp(ctx context.Context, appID string) ([]domain.Deployment, error)
}

type AppServiceConfig struct {
	Apps        AppRepository
	Deployments DeploymentRepository
}

type AppService struct {
	deployerv1.UnimplementedAppServiceServer
	apps        AppRepository
	deployments DeploymentRepository
	now         func() time.Time
}

func NewAppService(cfg AppServiceConfig) AppService {
	return AppService{
		apps:        cfg.Apps,
		deployments: cfg.Deployments,
		now:         time.Now,
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
	desiredStateJSON, err := cfg.JSON()
	if err != nil {
		return nil, status.Error(codes.Internal, "encode desired state")
	}

	now := s.now().UTC()
	app, err := s.apps.FindByName(ctx, cfg.Name)
	if errors.Is(err, db.ErrNotFound) {
		app = domain.App{
			ID:               newID("app"),
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

	deployment := domain.Deployment{
		ID:        newID("deploy"),
		AppID:     app.ID,
		Status:    deploymentStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.deployments.Create(ctx, deployment); err != nil {
		return nil, status.Error(codes.Internal, "create deployment")
	}

	return &deployerv1.DeployAppResponse{
		App:        protoApp(app),
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
	response := &deployerv1.ListAppsResponse{}
	for _, app := range apps {
		response.Apps = append(response.Apps, protoApp(app))
	}
	return response, nil
}

func (s AppService) InspectApp(ctx context.Context, req *deployerv1.InspectAppRequest) (*deployerv1.InspectAppResponse, error) {
	app, deployments, err := s.inspectApp(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	response := &deployerv1.InspectAppResponse{App: protoApp(app)}
	for _, deployment := range deployments {
		response.Deployments = append(response.Deployments, protoDeployment(deployment))
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
	app, err := s.apps.MarkDeleted(ctx, name, s.now().UTC())
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "app not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "delete app")
	}
	return &deployerv1.DeleteAppResponse{App: protoApp(app)}, nil
}

func (s AppService) GetApp(ctx context.Context, req *deployerv1.GetAppRequest) (*deployerv1.GetAppResponse, error) {
	app, deployments, err := s.inspectApp(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	response := &deployerv1.GetAppResponse{App: protoApp(app)}
	for _, deployment := range deployments {
		response.Deployments = append(response.Deployments, protoDeployment(deployment))
	}
	return response, nil
}

func (s AppService) GetAppStatus(ctx context.Context, req *deployerv1.GetAppStatusRequest) (*deployerv1.GetAppStatusResponse, error) {
	app, deployments, err := s.inspectApp(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	response := &deployerv1.GetAppStatusResponse{App: protoApp(app)}
	if len(deployments) > 0 {
		response.LatestDeployment = protoDeployment(deployments[0])
	}
	return response, nil
}

func (s AppService) inspectApp(ctx context.Context, name string) (domain.App, []domain.Deployment, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return domain.App{}, nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.App{}, nil, status.Error(codes.InvalidArgument, "name is required")
	}
	app, err := s.apps.FindActiveByName(ctx, name)
	if errors.Is(err, db.ErrNotFound) {
		return domain.App{}, nil, status.Error(codes.NotFound, "app not found")
	}
	if err != nil {
		return domain.App{}, nil, status.Error(codes.Internal, "get app")
	}
	deployments, err := s.deployments.ListByApp(ctx, app.ID)
	if err != nil {
		return domain.App{}, nil, status.Error(codes.Internal, "list app deployments")
	}
	return app, deployments, nil
}

func protoApp(app domain.App) *deployerv1.App {
	cfg, _ := appconfig.FromJSON(app.DesiredStateJSON)
	return &deployerv1.App{
		Id:           app.ID,
		Name:         app.Name,
		Image:        app.Image,
		Replicas:     int32(cfg.Deploy.Replicas),
		DesiredState: app.DesiredStateJSON,
		CreatedAt:    formatProtoTime(app.CreatedAt),
		UpdatedAt:    formatProtoTime(app.UpdatedAt),
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
