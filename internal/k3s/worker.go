package k3s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultNodeTokenPath = "/var/lib/rancher/k3s/server/node-token"

const raspberryPiCgroupHint = `memory cgroup is not enabled; on Raspberry Pi add "cgroup_memory=1 cgroup_enable=memory" to /boot/firmware/cmdline.txt and reboot before rerunning deployer-agent join-k3s`

type WorkerJoinProvider struct {
	NodeTokenPath string
	WireGuardIP   string
	ReadFile      func(string) ([]byte, error)
}

func NewWorkerJoinProvider(nodeTokenPath string, wireGuardIP string) WorkerJoinProvider {
	return WorkerJoinProvider{NodeTokenPath: nodeTokenPath, WireGuardIP: wireGuardIP, ReadFile: os.ReadFile}
}

func (p WorkerJoinProvider) WorkerJoinMaterial(ctx context.Context) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if net.ParseIP(strings.TrimSpace(p.WireGuardIP)) == nil {
		return "", "", errors.New("k3s WireGuard hub IP is not configured")
	}
	path := strings.TrimSpace(p.NodeTokenPath)
	if path == "" {
		path = DefaultNodeTokenPath
	}
	readFile := p.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	token, err := readFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read k3s worker token: %w", err)
	}
	if strings.TrimSpace(string(token)) == "" {
		return "", "", errors.New("k3s worker token is empty")
	}
	return "https://" + net.JoinHostPort(strings.TrimSpace(p.WireGuardIP), kubernetesAPIPort), strings.TrimSpace(string(token)), nil
}

type WorkerConfig struct {
	ServerURL        string
	Token            string
	NodeName         string
	NodeIP           string
	ConfigPath       string
	InstallerURL     string
	FlannelInterface string
}

func (c WorkerConfig) WithDefaults() WorkerConfig {
	if c.ConfigPath == "" {
		c.ConfigPath = DefaultConfigPath
	}
	if c.InstallerURL == "" {
		c.InstallerURL = DefaultInstallerURL
	}
	if c.FlannelInterface == "" {
		c.FlannelInterface = DefaultFlannelIface
	}
	return c
}

func (c WorkerConfig) Validate() error {
	errs := []error{}
	parsedURL, err := url.Parse(strings.TrimSpace(c.ServerURL))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		errs = append(errs, errors.New("k3s worker server URL must be a valid https URL"))
	}
	if strings.TrimSpace(c.Token) == "" {
		errs = append(errs, errors.New("k3s worker token is required"))
	}
	if strings.TrimSpace(c.NodeName) == "" {
		errs = append(errs, errors.New("k3s worker node name is required"))
	}
	if net.ParseIP(strings.TrimSpace(c.NodeIP)) == nil {
		errs = append(errs, errors.New("k3s worker node IP must be a valid IP address"))
	}
	if strings.TrimSpace(c.FlannelInterface) == "" {
		errs = append(errs, errors.New("k3s worker flannel interface is required"))
	}
	return errors.Join(errs...)
}

type workerConfigFile struct {
	Server           string `yaml:"server"`
	Token            string `yaml:"token"`
	NodeName         string `yaml:"node-name"`
	NodeIP           string `yaml:"node-ip"`
	FlannelInterface string `yaml:"flannel-iface"`
}

func WorkerConfigYAML(cfg WorkerConfig) ([]byte, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(workerConfigFile{
		Server: cfg.ServerURL, Token: cfg.Token, NodeName: cfg.NodeName, NodeIP: cfg.NodeIP,
		FlannelInterface: strings.TrimSpace(cfg.FlannelInterface),
	})
	if err != nil {
		return nil, fmt.Errorf("encode k3s worker config: %w", err)
	}
	return data, nil
}

func (b Bootstrapper) BootstrapWorker(ctx context.Context, cfg WorkerConfig) error {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	b = b.withDefaults()
	if b.Runtime.GOOS() != "linux" {
		return fmt.Errorf("k3s worker bootstrap requires Linux, got %s", b.Runtime.GOOS())
	}
	if b.Runtime.EUID() != 0 {
		return errors.New("k3s worker bootstrap must run as root; rerun with sudo or as root")
	}
	if err := b.validateMemoryCgroup(); err != nil {
		return err
	}
	data, err := WorkerConfigYAML(cfg)
	if err != nil {
		return err
	}
	if err := b.Files.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("create k3s worker config directory: %w", err)
	}
	if err := b.Files.WriteFile(cfg.ConfigPath, data, 0o600); err != nil {
		return fmt.Errorf("write k3s worker config: %w", err)
	}
	installerScript, err := b.downloadInstaller(ctx, cfg.InstallerURL)
	if err != nil {
		return err
	}
	env := []string{
		"INSTALL_K3S_EXEC=agent --config " + cfg.ConfigPath,
		"K3S_URL=" + cfg.ServerURL,
		"K3S_TOKEN=" + cfg.Token,
	}
	if err := b.Runner.Run(ctx, "/bin/sh", nil, env, installerScript); err != nil {
		return fmt.Errorf("run k3s worker installer: %w", err)
	}
	return nil
}

func (b Bootstrapper) validateMemoryCgroup() error {
	controllers, err := b.Files.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err == nil {
		for _, controller := range strings.Fields(string(controllers)) {
			if controller == "memory" {
				return nil
			}
		}
		return errors.New(raspberryPiCgroupHint)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect cgroup controllers: %w", err)
	}
	if _, err := b.Files.Stat("/sys/fs/cgroup/memory"); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect memory cgroup: %w", err)
	}
	return errors.New(raspberryPiCgroupHint)
}
