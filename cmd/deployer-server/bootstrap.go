package main

import (
	"context"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/server"
)

func bootstrap() int {
	cfg := config.LoadServer()
	if cfg.DatabaseURL == "" {
		fmt.Fprintln(os.Stderr, "DEPLOYER_DATABASE_URL is required")
		return 1
	}
	if cfg.TokenHashKey == "" {
		fmt.Fprintln(os.Stderr, "DEPLOYER_TOKEN_HASH_KEY is required")
		return 1
	}

	ctx := context.Background()
	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer database.Close()

	adminTokens := db.NewAdminTokenRepository(database)
	token, err := server.BootstrapAdminToken(ctx, adminTokens, cfg.TokenHashKey, "bootstrap")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Fprintln(os.Stdout, "Admin token:")
	fmt.Fprintln(os.Stdout, token)
	return 0
}
