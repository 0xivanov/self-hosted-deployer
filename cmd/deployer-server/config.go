package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/joho/godotenv"
)

func loadDotEnv() error {
	err := godotenv.Load()
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("load .env: %w", err)
}

func validateServeConfig(cfg config.ServerConfig) error {
	var errs []error
	if cfg.GRPCListenAddress == "" {
		errs = append(errs, errors.New("DEPLOYER_SERVER_GRPC_ADDR is required"))
	}
	if cfg.HTTPListenAddress == "" {
		errs = append(errs, errors.New("DEPLOYER_SERVER_HTTP_ADDR is required"))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DEPLOYER_DATABASE_URL is required"))
	}
	if cfg.TokenHashKey == "" {
		errs = append(errs, errors.New("DEPLOYER_TOKEN_HASH_KEY is required"))
	}
	return errors.Join(errs...)
}
