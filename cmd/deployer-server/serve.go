package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/logging"
	"github.com/0xivanov/self-hosted-deployer/internal/server"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func serve() int {
	logger := logging.New("deployer-server", os.Getenv("DEPLOYER_LOG_LEVEL"))
	logger.Info("server starting", "version", version.Version, "commit", version.Commit)

	cfg := config.LoadServer()
	if err := validateServeConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, config.FormatValidationError("server", err))
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer database.Close()

	if err := server.Serve(ctx, cfg, logger, newRepositories(database)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
