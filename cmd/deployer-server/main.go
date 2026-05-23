package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/logging"
	"github.com/0xivanov/self-hosted-deployer/internal/server"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
	"github.com/joho/godotenv"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	flags := flag.NewFlagSet("deployer-server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	showVersion := flags.Bool("version", false, "print version information")
	validateConfig := flags.Bool("validate-config", false, "validate server configuration")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion || (flags.NArg() > 0 && flags.Arg(0) == "version") {
		fmt.Println(version.Current().String())
		return 0
	}

	if *validateConfig {
		cfg := config.LoadServer()
		if err := config.FormatValidationError("server", validateServeConfig(cfg)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "server config ok")
		return 0
	}

	if flags.NArg() > 0 && flags.Arg(0) == "bootstrap" {
		return bootstrap()
	}

	if flags.NArg() > 0 && flags.Arg(0) == "help" {
		usage()
		return 0
	}

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

func loadDotEnv() error {
	err := godotenv.Load()
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("load .env: %w", err)
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: deployer-server [--version] [--validate-config] [bootstrap|help]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  bootstrap  Create an initial admin token and print it once")
	fmt.Fprintln(os.Stderr, "  version    Print version information")
	fmt.Fprintln(os.Stderr, "  help       Show help")
}

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

func newRepositories(database *db.Db) server.Repositories {
	return server.Repositories{
		Health:      db.NewHealthRepository(database),
		AdminTokens: db.NewAdminTokenRepository(database),
		AgentTokens: db.NewAgentTokenRepository(database),
		JoinTokens:  db.NewJoinTokenRepository(database),
	}
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
