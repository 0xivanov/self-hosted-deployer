package server

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func UnaryLoggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		startedAt := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)
		service, method := splitMethod(info.FullMethod)
		logger.Info("grpc request",
			"service", service,
			"method", method,
			"status", code.String(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
		return resp, err
	}
}

func StreamLoggingInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		startedAt := time.Now()
		err := handler(srv, stream)
		code := status.Code(err)
		service, method := splitMethod(info.FullMethod)
		logger.Info("grpc stream",
			"service", service,
			"method", method,
			"status", code.String(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
		return err
	}
}

func splitMethod(fullMethod string) (string, string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	service, method, ok := strings.Cut(trimmed, "/")
	if !ok {
		return trimmed, ""
	}
	return service, method
}
