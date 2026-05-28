package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/k3s"
	"github.com/0xivanov/self-hosted-deployer/internal/server"
)

var newK3sBootstrapper = k3s.NewBootstrapper
var randomReader io.Reader = rand.Reader

func bootstrap(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "server":
			return bootstrapServer(args[1:])
		case "admin":
			return bootstrapAdmin()
		case "k3s":
			return bootstrapK3s(args[1:])
		case "help":
			bootstrapUsage()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "unknown bootstrap command %q\n", args[0])
			bootstrapUsage()
			return 2
		}
	}
	return bootstrapServer(nil)
}

type serverBootstrapOptions struct {
	EnvFile               string
	DatabaseURL           string
	PublicBaseURL         string
	GRPCAddress           string
	HTTPAddress           string
	KubeconfigPath        string
	IngressNamespace      string
	K3sWireGuardIP        string
	WireGuardInterface    string
	WireGuardHubPublicKey string
	WireGuardEndpoint     string
	Force                 bool
}

func bootstrapServer(args []string) int {
	cfg := config.LoadServer()
	opts := serverBootstrapOptions{
		EnvFile:               "/etc/deployer/server.env",
		DatabaseURL:           "file:/var/lib/deployer/deployer.db",
		PublicBaseURL:         cfg.PublicBaseURL,
		GRPCAddress:           cfg.GRPCListenAddress,
		HTTPAddress:           cfg.HTTPListenAddress,
		KubeconfigPath:        cfg.KubeconfigPath,
		IngressNamespace:      cfg.IngressNamespace,
		K3sWireGuardIP:        cfg.K3sWireGuardIP,
		WireGuardInterface:    cfg.WireGuardInterface,
		WireGuardHubPublicKey: cfg.WireGuardHubPublicKey,
		WireGuardEndpoint:     cfg.WireGuardEndpoint,
	}

	flags := flag.NewFlagSet("deployer-server bootstrap server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&opts.EnvFile, "env-file", opts.EnvFile, "path to write the server environment file")
	flags.StringVar(&opts.DatabaseURL, "database-url", opts.DatabaseURL, "SQLite database URL for the control plane")
	flags.StringVar(&opts.PublicBaseURL, "public-base-url", opts.PublicBaseURL, "public base URL for generated agent commands")
	flags.StringVar(&opts.GRPCAddress, "grpc-addr", opts.GRPCAddress, "gRPC listen address")
	flags.StringVar(&opts.HTTPAddress, "http-addr", opts.HTTPAddress, "HTTP health listen address")
	flags.StringVar(&opts.KubeconfigPath, "kubeconfig", opts.KubeconfigPath, "kubeconfig path for the local k3s cluster")
	flags.StringVar(&opts.IngressNamespace, "ingress-namespace", opts.IngressNamespace, "Kubernetes namespace for managed app resources")
	flags.StringVar(&opts.K3sWireGuardIP, "k3s-wireguard-ip", opts.K3sWireGuardIP, "VPS WireGuard hub IP for the k3s API")
	flags.StringVar(&opts.WireGuardInterface, "wireguard-interface", opts.WireGuardInterface, "WireGuard hub interface name")
	flags.StringVar(&opts.WireGuardHubPublicKey, "wireguard-hub-public-key", opts.WireGuardHubPublicKey, "WireGuard hub public key")
	flags.StringVar(&opts.WireGuardEndpoint, "wireguard-endpoint", opts.WireGuardEndpoint, "public WireGuard endpoint host:port")
	flags.BoolVar(&opts.Force, "force", false, "overwrite an existing env file and create a fresh admin token")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: deployer-server bootstrap server [--env-file path] [--database-url url] [--public-base-url url] [--force]")
		return 2
	}

	result, err := runServerBootstrap(context.Background(), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Fprintln(os.Stdout, "Server environment written:")
	fmt.Fprintln(os.Stdout, result.EnvFile)
	fmt.Fprintln(os.Stdout, "Admin token:")
	fmt.Fprintln(os.Stdout, result.AdminToken)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Next steps:")
	fmt.Fprintf(os.Stdout, "  install deploy/systemd/deployer-server.service to /etc/systemd/system/deployer-server.service\n")
	fmt.Fprintf(os.Stdout, "  run deployer-server bootstrap k3s --wireguard-ip <hub-ip>\n")
	fmt.Fprintf(os.Stdout, "  start the service with systemctl enable --now deployer-server\n")
	return 0
}

type serverBootstrapResult struct {
	EnvFile    string
	AdminToken string
}

func runServerBootstrap(ctx context.Context, opts serverBootstrapOptions) (serverBootstrapResult, error) {
	if opts.EnvFile == "" {
		return serverBootstrapResult{}, errors.New("--env-file is required")
	}
	if opts.DatabaseURL == "" {
		return serverBootstrapResult{}, errors.New("--database-url is required")
	}
	if !opts.Force {
		if _, err := os.Stat(opts.EnvFile); err == nil {
			return serverBootstrapResult{}, fmt.Errorf("%s already exists; rerun with --force to replace it", opts.EnvFile)
		} else if !errors.Is(err, os.ErrNotExist) {
			return serverBootstrapResult{}, fmt.Errorf("inspect existing env file: %w", err)
		}
	}
	secretKey, err := randomEnvKey()
	if err != nil {
		return serverBootstrapResult{}, fmt.Errorf("generate secret key: %w", err)
	}
	tokenHashKey, err := randomEnvKey()
	if err != nil {
		return serverBootstrapResult{}, fmt.Errorf("generate token hash key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(opts.EnvFile), 0o700); err != nil {
		return serverBootstrapResult{}, fmt.Errorf("create env directory: %w", err)
	}
	if dbPath, ok := sqliteDatabasePath(opts.DatabaseURL); ok {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
			return serverBootstrapResult{}, fmt.Errorf("create database directory: %w", err)
		}
	}
	database, err := db.Open(ctx, opts.DatabaseURL)
	if err != nil {
		return serverBootstrapResult{}, err
	}
	defer database.Close()

	env, err := renderServerEnv(opts, secretKey, tokenHashKey)
	if err != nil {
		return serverBootstrapResult{}, err
	}
	if err := os.WriteFile(opts.EnvFile, []byte(env), 0o600); err != nil {
		return serverBootstrapResult{}, fmt.Errorf("write server env file: %w", err)
	}

	token, err := server.BootstrapAdminToken(ctx, db.NewAdminTokenRepository(database), tokenHashKey, "bootstrap")
	if err != nil {
		return serverBootstrapResult{}, err
	}
	return serverBootstrapResult{EnvFile: opts.EnvFile, AdminToken: token}, nil
}

func randomEnvKey() (string, error) {
	data := make([]byte, 24)
	if _, err := io.ReadFull(randomReader, data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func renderServerEnv(opts serverBootstrapOptions, secretKey string, tokenHashKey string) (string, error) {
	values := [][2]string{
		{"DEPLOYER_SERVER_GRPC_ADDR", opts.GRPCAddress},
		{"DEPLOYER_SERVER_HTTP_ADDR", opts.HTTPAddress},
		{"DEPLOYER_DATABASE_URL", opts.DatabaseURL},
		{"DEPLOYER_PUBLIC_BASE_URL", opts.PublicBaseURL},
		{"DEPLOYER_SECRET_KEY", secretKey},
		{"DEPLOYER_TOKEN_HASH_KEY", tokenHashKey},
		{"DEPLOYER_KUBECONFIG", opts.KubeconfigPath},
		{"DEPLOYER_INGRESS_NAMESPACE", opts.IngressNamespace},
		{"DEPLOYER_K3S_WIREGUARD_IP", opts.K3sWireGuardIP},
		{"DEPLOYER_WIREGUARD_INTERFACE", opts.WireGuardInterface},
		{"DEPLOYER_WIREGUARD_HUB_PUBLIC_KEY", opts.WireGuardHubPublicKey},
		{"DEPLOYER_WIREGUARD_ENDPOINT", opts.WireGuardEndpoint},
	}
	var builder strings.Builder
	builder.WriteString("# Generated by deployer-server bootstrap server. Keep this file mode 0600.\n")
	for _, value := range values {
		line, err := envLine(value[0], value[1])
		if err != nil {
			return "", err
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String(), nil
}

func envLine(key string, value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("%s cannot contain newlines", key)
	}
	return key + "=" + value, nil
}

func sqliteDatabasePath(dsn string) (string, bool) {
	value := strings.TrimSpace(dsn)
	if value == "" || value == ":memory:" || strings.HasPrefix(value, "file::memory:") {
		return "", false
	}
	if strings.HasPrefix(value, "file:") {
		value = strings.TrimPrefix(value, "file:")
		if before, _, ok := strings.Cut(value, "?"); ok {
			value = before
		}
	}
	if value == "" {
		return "", false
	}
	return value, true
}

func bootstrapAdmin() int {
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

func bootstrapK3s(args []string) int {
	cfg := config.LoadServer()
	flags := flag.NewFlagSet("deployer-server bootstrap k3s", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	wireGuardIP := flags.String("wireguard-ip", cfg.K3sWireGuardIP, "VPS WireGuard hub IP for the k3s API")
	configPath := flags.String("config-path", cfg.K3sConfigPath, "path to write the k3s server config")
	kubeconfigPath := flags.String("kubeconfig", cfg.KubeconfigPath, "path where k3s writes kubeconfig")
	installerURL := flags.String("installer-url", cfg.K3sInstallerURL, "official k3s installer URL")
	force := flags.Bool("force", false, "reapply config and installer when k3s appears to be installed")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: deployer-server bootstrap k3s [--wireguard-ip ip] [--config-path path] [--kubeconfig path] [--installer-url url] [--force]")
		return 2
	}

	result, err := newK3sBootstrapper().Bootstrap(context.Background(), k3s.Config{
		WireGuardIP:    *wireGuardIP,
		ConfigPath:     *configPath,
		KubeconfigPath: *kubeconfigPath,
		InstallerURL:   *installerURL,
		Force:          *force,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if result.ExistingInstall {
		fmt.Fprintln(os.Stdout, "k3s configuration reapplied")
	} else {
		fmt.Fprintln(os.Stdout, "k3s server bootstrapped")
	}
	fmt.Fprintf(os.Stdout, "Config: %s\n", result.ConfigPath)
	fmt.Fprintf(os.Stdout, "Kubeconfig: %s\n", result.KubeconfigPath)
	fmt.Fprintf(os.Stdout, "WireGuard API: %s\n", formatK3sAPIURL(result.WireGuardIP))
	return 0
}

func formatK3sAPIURL(host string) string {
	return "https://" + net.JoinHostPort(host, "6443")
}

func bootstrapUsage() {
	fmt.Fprintln(os.Stderr, "Usage: deployer-server bootstrap [server|admin|k3s|help]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  server Create a server env file, initialize the DB, and print an admin token")
	fmt.Fprintln(os.Stderr, "  admin  Create an initial admin token and print it once")
	fmt.Fprintln(os.Stderr, "  k3s    Install and start the VPS k3s server")
	fmt.Fprintln(os.Stderr, "  help   Show bootstrap help")
}
