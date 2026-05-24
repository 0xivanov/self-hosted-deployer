package k3s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigPath     = "/etc/rancher/k3s/config.yaml"
	DefaultKubeconfigPath = "/etc/rancher/k3s/k3s.yaml"
	DefaultInstallerURL   = "https://get.k3s.io"
	kubernetesAPIPort     = "6443"
	maxInstallerBytes     = 4 << 20
)

type Config struct {
	WireGuardIP    string
	ConfigPath     string
	KubeconfigPath string
	InstallerURL   string
	Force          bool
}

func (c Config) WithDefaults() Config {
	if c.ConfigPath == "" {
		c.ConfigPath = DefaultConfigPath
	}
	if c.KubeconfigPath == "" {
		c.KubeconfigPath = DefaultKubeconfigPath
	}
	if c.InstallerURL == "" {
		c.InstallerURL = DefaultInstallerURL
	}
	return c
}

func (c Config) Validate() error {
	errs := []error{}
	if net.ParseIP(strings.TrimSpace(c.WireGuardIP)) == nil {
		errs = append(errs, errors.New("DEPLOYER_K3S_WIREGUARD_IP must be a valid IP address"))
	}
	if strings.TrimSpace(c.ConfigPath) == "" {
		errs = append(errs, errors.New("DEPLOYER_K3S_CONFIG_PATH is required"))
	}
	if strings.TrimSpace(c.KubeconfigPath) == "" {
		errs = append(errs, errors.New("DEPLOYER_KUBECONFIG is required"))
	}
	if strings.TrimSpace(c.InstallerURL) == "" {
		errs = append(errs, errors.New("DEPLOYER_K3S_INSTALLER_URL is required"))
	}
	return errors.Join(errs...)
}

type Result struct {
	ConfigPath      string
	KubeconfigPath  string
	WireGuardIP     string
	ExistingInstall bool
}

type Bootstrapper struct {
	Runtime     Runtime
	Files       FileSystem
	Runner      CommandRunner
	HTTPClient  HTTPDoer
	PortChecker PortChecker
}

func NewBootstrapper() Bootstrapper {
	return Bootstrapper{
		Runtime:     osRuntime{},
		Files:       osFileSystem{},
		Runner:      execRunner{},
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		PortChecker: netPortChecker{},
	}
}

func (b Bootstrapper) Bootstrap(ctx context.Context, cfg Config) (Result, error) {
	cfg = cfg.WithDefaults()
	result := Result{
		ConfigPath:     cfg.ConfigPath,
		KubeconfigPath: cfg.KubeconfigPath,
		WireGuardIP:    cfg.WireGuardIP,
	}
	if err := cfg.Validate(); err != nil {
		return result, err
	}

	b = b.withDefaults()
	if err := b.validateHost(ctx, cfg); err != nil {
		return result, err
	}

	existingReasons, err := b.detectExistingInstall(cfg)
	if err != nil {
		return result, err
	}
	if len(existingReasons) > 0 {
		result.ExistingInstall = true
		if !cfg.Force {
			return result, fmt.Errorf("k3s already appears installed (%s); inspect the host or rerun with --force to reapply configuration", strings.Join(existingReasons, ", "))
		}
	}

	if !result.ExistingInstall {
		if err := b.PortChecker.Check(ctx, cfg.WireGuardIP, kubernetesAPIPort); err != nil {
			return result, fmt.Errorf("validate k3s API listen address %s: %w", net.JoinHostPort(cfg.WireGuardIP, kubernetesAPIPort), err)
		}
	}
	if err := b.writeConfig(cfg); err != nil {
		return result, err
	}

	installerScript, err := b.downloadInstaller(ctx, cfg.InstallerURL)
	if err != nil {
		return result, err
	}
	installerEnv := []string{"INSTALL_K3S_EXEC=server --config " + cfg.ConfigPath}
	if err := b.Runner.Run(ctx, "/bin/sh", nil, installerEnv, installerScript); err != nil {
		return result, fmt.Errorf("run k3s installer: %w", err)
	}
	if err := b.Runner.Run(ctx, "systemctl", []string{"enable", "--now", "k3s"}, nil, nil); err != nil {
		return result, fmt.Errorf("start k3s service: %w", err)
	}

	return result, nil
}

func (b Bootstrapper) withDefaults() Bootstrapper {
	if b.Runtime == nil {
		b.Runtime = osRuntime{}
	}
	if b.Files == nil {
		b.Files = osFileSystem{}
	}
	if b.Runner == nil {
		b.Runner = execRunner{}
	}
	if b.HTTPClient == nil {
		b.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if b.PortChecker == nil {
		b.PortChecker = netPortChecker{}
	}
	return b
}

func (b Bootstrapper) validateHost(ctx context.Context, cfg Config) error {
	if b.Runtime.GOOS() != "linux" {
		return fmt.Errorf("k3s bootstrap requires Linux, got %s", b.Runtime.GOOS())
	}
	if b.Runtime.EUID() != 0 {
		return errors.New("k3s bootstrap must run as root; rerun with sudo or as root")
	}
	if err := b.PortChecker.Check(ctx, cfg.WireGuardIP, "0"); err != nil {
		return fmt.Errorf("WireGuard hub IP %s is not configured locally or cannot be bound: %w", cfg.WireGuardIP, err)
	}
	return nil
}

func (b Bootstrapper) detectExistingInstall(cfg Config) ([]string, error) {
	reasons := []string{}
	if path, err := b.Runtime.LookPath("k3s"); err == nil {
		reasons = append(reasons, "k3s binary at "+path)
	} else if !errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("detect k3s binary: %w", err)
	}
	if _, err := b.Files.Stat(cfg.KubeconfigPath); err == nil {
		reasons = append(reasons, "kubeconfig at "+cfg.KubeconfigPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("detect kubeconfig: %w", err)
	}
	return reasons, nil
}

func (b Bootstrapper) writeConfig(cfg Config) error {
	data, err := ServerConfigYAML(cfg)
	if err != nil {
		return err
	}
	if err := b.Files.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("create k3s config directory: %w", err)
	}
	if err := b.Files.WriteFile(cfg.ConfigPath, data, 0o600); err != nil {
		return fmt.Errorf("write k3s config: %w", err)
	}
	return nil
}

func (b Bootstrapper) downloadInstaller(ctx context.Context, installerURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, installerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prepare k3s installer request: %w", err)
	}
	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download k3s installer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download k3s installer: unexpected HTTP status %s", resp.Status)
	}
	var buf bytes.Buffer
	limited := io.LimitReader(resp.Body, maxInstallerBytes+1)
	if _, err := buf.ReadFrom(limited); err != nil {
		return nil, fmt.Errorf("read k3s installer: %w", err)
	}
	if buf.Len() > maxInstallerBytes {
		return nil, fmt.Errorf("read k3s installer: response exceeds %d bytes", maxInstallerBytes)
	}
	return buf.Bytes(), nil
}

type serverConfigFile struct {
	BindAddress         string   `yaml:"bind-address"`
	AdvertiseAddress    string   `yaml:"advertise-address"`
	NodeIP              string   `yaml:"node-ip"`
	TLSSAN              []string `yaml:"tls-san"`
	WriteKubeconfig     string   `yaml:"write-kubeconfig"`
	WriteKubeconfigMode string   `yaml:"write-kubeconfig-mode"`
}

func ServerConfigYAML(cfg Config) ([]byte, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(serverConfigFile{
		BindAddress:         cfg.WireGuardIP,
		AdvertiseAddress:    cfg.WireGuardIP,
		NodeIP:              cfg.WireGuardIP,
		TLSSAN:              []string{cfg.WireGuardIP},
		WriteKubeconfig:     cfg.KubeconfigPath,
		WriteKubeconfigMode: "0644",
	})
	if err != nil {
		return nil, fmt.Errorf("encode k3s config: %w", err)
	}
	return data, nil
}

type Runtime interface {
	GOOS() string
	EUID() int
	LookPath(file string) (string, error)
}

type FileSystem interface {
	Stat(name string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args []string, env []string, stdin []byte) error
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type PortChecker interface {
	Check(ctx context.Context, host string, port string) error
}

type osRuntime struct{}

func (osRuntime) GOOS() string {
	return runtime.GOOS
}

func (osRuntime) EUID() int {
	return os.Geteuid()
}

func (osRuntime) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

type osFileSystem struct{}

func (osFileSystem) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

func (osFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (osFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args []string, env []string, stdin []byte) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

type netPortChecker struct{}

func (netPortChecker) Check(ctx context.Context, host string, port string) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	return listener.Close()
}
