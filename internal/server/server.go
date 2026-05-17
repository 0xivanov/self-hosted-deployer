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
	"github.com/0xivanov/self-hosted-deployer/internal/repository"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func Run(ctx context.Context, cfg config.ServerConfig, logger *slog.Logger) error {
	repo, err := repository.OpenSQLite(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer repo.Close()
	return Serve(ctx, cfg, logger, repo)
}

func Serve(ctx context.Context, cfg config.ServerConfig, logger *slog.Logger, repo repository.Repository) error {
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

	auth := NewAuthenticator(repo, cfg.TokenHashKey)
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			UnaryLoggingInterceptor(logger),
			auth.UnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			StreamLoggingInterceptor(logger),
		),
	)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	deployerv1.RegisterPlatformServiceServer(grpcServer, NewPlatformService(repo))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := repo.Ping(r.Context()); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
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
		_ = httpServer.Shutdown(shutdownCtx)
		grpcServer.GracefulStop()
		return nil
	case err := <-errs:
		grpcServer.Stop()
		_ = httpServer.Close()
		return err
	}
}
