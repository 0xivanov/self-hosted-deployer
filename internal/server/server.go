package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type Repositories struct {
	RegistryCredentials RegistryCredentialRepository
	EnvironmentBundles  EnvironmentBundleRepository
	Health              HealthRepository
	AdminTokens         AdminTokenRepository
	AgentTokens         AgentTokenRepository
	JoinTokens          JoinTokenRepository
	Nodes               NodeRepository
	Apps                AppRepository
	Deployments         DeploymentRepository
	Routes              RouteRepository
	Secrets             SecretRepository
	Events              EventRepository
}

type Runtime struct {
	Apps            AppRuntime
	Ingress         IngressRuntime
	Nodes           NodeRuntime
	Readiness       ReadinessRuntime
	WireGuardPeers  PeerSynchronizer
	WorkerJoin      WorkerJoinMaterialProvider
	SecretCipher    SecretCipher
	RouteTLSEnabled bool
}

type ReadinessRuntime interface {
	Ready(ctx context.Context) error
}

func Serve(ctx context.Context, cfg config.ServerConfig, logger *slog.Logger, repos Repositories, runtime Runtime) error {
	serverIdentity, err := LoadServerIdentity(cfg)
	if err != nil {
		return fmt.Errorf("load server identity: %w", err)
	}
	retention, err := cfg.EventRetention()
	if err != nil {
		return fmt.Errorf("configure event retention: %w", err)
	}
	nodeMonitor, err := cfg.NodeMonitor()
	if err != nil {
		return fmt.Errorf("configure node monitor: %w", err)
	}
	// Restore volatile hub state before accepting enrollment/removal RPCs,
	// so a startup snapshot cannot race with those mutations.
	if err := restoreHubPeers(ctx, cfg, repos.Nodes, runtime.WireGuardPeers); err != nil {
		return fmt.Errorf("restore WireGuard hub peers: %w", err)
	}
	grpcListener, err := net.Listen("tcp", cfg.GRPCListenAddress)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}
	defer grpcListener.Close()

	httpListener, err := net.Listen("tcp", cfg.HTTPListenAddress)
	if err != nil {
		return fmt.Errorf("listen http: %w", err)
	}
	defer httpListener.Close()

	auth := NewAuthenticator(TokenRepositories{
		AdminTokens: repos.AdminTokens,
		AgentTokens: repos.AgentTokens,
		JoinTokens:  repos.JoinTokens,
	}, cfg.TokenHashKey)
	grpcOptions := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			UnaryLoggingInterceptor(logger),
			auth.UnaryInterceptor(),
			NewMutationAuditInterceptor(newSlogMutationAuditSink(logger)),
		),
		grpc.ChainStreamInterceptor(
			StreamLoggingInterceptor(logger),
			auth.StreamInterceptor(),
		),
	}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		serverCredentials, err := credentials.NewServerTLSFromFile(
			cfg.TLSCertFile,
			cfg.TLSKeyFile,
		)
		if err != nil {
			return fmt.Errorf("load grpc tls credentials: %w", err)
		}
		grpcOptions = append(grpcOptions, grpc.Creds(serverCredentials))
	}
	grpcServer := grpc.NewServer(grpcOptions...)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	deployerv1.RegisterPlatformServiceServer(grpcServer, NewPlatformService(repos.Health, serverIdentity))
	eventRecorder := NewEventRecorder(repos.Events, logger)
	deployerv1.RegisterNodeServiceServer(grpcServer, NewNodeService(NodeServiceConfig{
		Nodes:        repos.Nodes,
		JoinTokens:   repos.JoinTokens,
		AgentTokens:  repos.AgentTokens,
		TokenHashKey: cfg.TokenHashKey,
		Events:       eventRecorder,
		Runtime:      runtime.Nodes,
		Peers:        runtime.WireGuardPeers,
		WorkerJoin:   runtime.WorkerJoin,
		Network: WorkerNetworkConfig{
			Subnet:       cfg.WireGuardSubnet,
			HubIP:        cfg.K3sWireGuardIP,
			HubPublicKey: cfg.WireGuardHubPublicKey,
			Endpoint:     cfg.WireGuardEndpoint,
		},
		OfflineAfter: nodeMonitor.OfflineAfter,
	}))
	appRuntime := runtime.Apps
	if appRuntime == nil {
		appRuntime = runtime.Ingress
	}
	registryCredentials := NewRegistryCredentialService(repos.RegistryCredentials, runtime.SecretCipher)
	deployerv1.RegisterRegistryCredentialServiceServer(grpcServer, registryCredentials)
	deployerv1.RegisterEnvironmentServiceServer(grpcServer, NewEnvironmentBundleService(repos.EnvironmentBundles, runtime.SecretCipher))
	deployerv1.RegisterAppServiceServer(grpcServer, NewAppService(AppServiceConfig{
		RegistryCredentials:          registryCredentials,
		RegistryCredentialRepository: repos.RegistryCredentials,
		EnvironmentBundles:           repos.EnvironmentBundles,
		Apps:                         repos.Apps,
		Deployments:                  repos.Deployments,
		Routes:                       repos.Routes,
		Secrets:                      repos.Secrets,
		Cipher:                       runtime.SecretCipher,
		Runtime:                      appRuntime,
		RouteTLSEnabled:              runtime.RouteTLSEnabled,
		Events:                       eventRecorder,
	}))
	deployerv1.RegisterSecretServiceServer(grpcServer, NewSecretService(SecretServiceConfig{
		RegistryCredentials: registryCredentials,
		Apps:                repos.Apps,
		Secrets:             repos.Secrets,
		Cipher:              runtime.SecretCipher,
		Runtime:             appRuntime,
		Events:              eventRecorder,
	}))
	deployerv1.RegisterEventServiceServer(grpcServer, NewEventService(EventServiceConfig{
		Events: repos.Events,
		Apps:   repos.Apps,
		Nodes:  repos.Nodes,
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writePlainText(logger, w, http.StatusOK, "ok\n")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := repos.Health.Ping(r.Context()); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		if runtime.Readiness != nil {
			if err := runtime.Readiness.Ready(r.Context()); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		writePlainText(logger, w, http.StatusOK, "ready\n")
	})
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 2)
	go RunEventRetention(ctx, repos.Events, retention, logger)
	go RunNodeOfflineMonitor(ctx, repos.Nodes, eventRecorder, logger, nodeMonitor.OfflineAfter, nodeMonitor.Interval)
	go func() {
		logger.Info("grpc server listening", "address", grpcListener.Addr().String())
		if err := grpcServer.Serve(grpcListener); err != nil {
			errs <- fmt.Errorf("serve grpc: %w", err)
		}
	}()
	go func() {
		logger.Info("http health server listening", "address", httpListener.Addr().String())
		if err := httpServer.Serve(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("serve http: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		grpcServer.GracefulStop()
		if shutdownErr != nil {
			return fmt.Errorf("shutdown http: %w", shutdownErr)
		}
		return nil
	case err := <-errs:
		grpcServer.Stop()
		if closeErr := httpServer.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			return errors.Join(err, fmt.Errorf("close http: %w", closeErr))
		}
		return err
	}
}

func restoreHubPeers(ctx context.Context, cfg config.ServerConfig, nodes NodeRepository, peers PeerSynchronizer) error {
	// A server without configured worker networking keeps its legacy startup
	// behavior and must not modify an unrelated default wg0 interface.
	if peers == nil || strings.TrimSpace(cfg.K3sWireGuardIP) == "" ||
		strings.TrimSpace(cfg.WireGuardHubPublicKey) == "" || strings.TrimSpace(cfg.WireGuardEndpoint) == "" {
		return nil
	}
	if nodes == nil {
		return errors.New("node repository is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	known, err := nodes.List(ctx)
	if err != nil {
		return fmt.Errorf("list saved nodes: %w", err)
	}
	return peers.SyncPeers(ctx, known)
}

func writePlainText(logger *slog.Logger, w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	if _, err := w.Write([]byte(body)); err != nil {
		logger.Warn("write http response", "error", err)
	}
}
