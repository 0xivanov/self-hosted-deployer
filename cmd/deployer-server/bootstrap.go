package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/k3s"
	"github.com/0xivanov/self-hosted-deployer/internal/server"
)

var newK3sBootstrapper = k3s.NewBootstrapper

func bootstrap(args []string) int {
	if len(args) > 0 {
		switch args[0] {
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
	return bootstrapAdmin()
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
	fmt.Fprintln(os.Stderr, "Usage: deployer-server bootstrap [admin|k3s|help]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  admin  Create an initial admin token and print it once")
	fmt.Fprintln(os.Stderr, "  k3s    Install and start the VPS k3s server")
	fmt.Fprintln(os.Stderr, "  help   Show bootstrap help")
}
