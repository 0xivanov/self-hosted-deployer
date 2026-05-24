package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type Repositories struct {
	Health      HealthRepository
	AdminTokens AdminTokenRepository
	AgentTokens AgentTokenRepository
	JoinTokens  JoinTokenRepository
	Nodes       NodeRepository
	Apps        AppRepository
	Deployments DeploymentRepository
}

func Serve(ctx context.Context, cfg config.ServerConfig, logger *slog.Logger, repos Repositories) error {
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
		),
		grpc.ChainStreamInterceptor(
			StreamLoggingInterceptor(logger),
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
	deployerv1.RegisterPlatformServiceServer(grpcServer, NewPlatformService(repos.Health))
	deployerv1.RegisterNodeServiceServer(grpcServer, NewNodeService(NodeServiceConfig{
		Nodes:        repos.Nodes,
		JoinTokens:   repos.JoinTokens,
		AgentTokens:  repos.AgentTokens,
		TokenHashKey: cfg.TokenHashKey,
	}))
	deployerv1.RegisterAppServiceServer(grpcServer, NewAppService(AppServiceConfig{
		Apps:        repos.Apps,
		Deployments: repos.Deployments,
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
		writePlainText(logger, w, http.StatusOK, "ready\n")
	})
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 2)
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

func writePlainText(logger *slog.Logger, w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	if _, err := w.Write([]byte(body)); err != nil {
		logger.Warn("write http response", "error", err)
	}
}
