package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/logging"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

const (
	initialHeartbeatBackoff = 5 * time.Second
	maxHeartbeatBackoff     = time.Minute
)

var (
	agentStdout         = io.Writer(os.Stdout)
	agentStderr         = io.Writer(os.Stderr)
	cachedKernelVersion = detectKernelVersion()
	newAgentClient      = func(serverURL string, token string) (agentClient, func() error, error) {
		client, conn, err := clicore.NewPlatformClient(serverURL, token)
		if err != nil {
			return nil, nil, err
		}
		return client, conn.Close, nil
	}
)

type agentClient interface {
	JoinNode(ctx context.Context, joinToken string, hostname string, arch string) (clicore.JoinResult, error)
	Heartbeat(ctx context.Context, heartbeat clicore.Heartbeat) error
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("deployer-agent", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	showVersion := flags.Bool("version", false, "print version information")
	validateConfig := flags.Bool("validate-config", false, "validate agent configuration")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion || (flags.NArg() > 0 && flags.Arg(0) == "version") {
		fmt.Fprintln(agentStdout, version.Current().String())
		return 0
	}

	if *validateConfig {
		cfg := config.LoadAgent()
		if err := config.FormatValidationError("agent", cfg.Validate()); err != nil {
			fmt.Fprintln(agentStderr, err)
			return 1
		}
		fmt.Fprintln(agentStdout, "agent config ok")
		return 0
	}

	if flags.NArg() > 0 {
		switch flags.Arg(0) {
		case "join":
			return join(flags.Args()[1:])
		case "run":
			return runLoop(flags.Args()[1:])
		case "help":
			usage()
			return 0
		}
	}

	logger := logging.New("deployer-agent", os.Getenv("DEPLOYER_LOG_LEVEL"))
	logger.Info("agent starting", "version", version.Version, "commit", version.Commit)
	usage()
	return 0
}

func join(args []string) int {
	cfg := config.LoadAgent()
	flags := flag.NewFlagSet("deployer-agent join", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	serverURL := flags.String("server", cfg.ServerURL, "control plane server URL")
	joinToken := flags.String("token", "", "node join token")
	credentialPath := flags.String("credential-path", cfg.NodeCredentialPath, "path to store the agent credential")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(*serverURL) == "" || strings.TrimSpace(*joinToken) == "" {
		fmt.Fprintln(agentStderr, "usage: deployer-agent join --server <url> --token <join-token>")
		return 2
	}

	client, closeClient, err := newAgentClient(*serverURL, "")
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	defer closeClient()

	hostname, _ := os.Hostname()
	result, err := client.JoinNode(context.Background(), strings.TrimSpace(*joinToken), hostname, runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	if err := writeCredential(*credentialPath, result.AgentToken); err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	fmt.Fprintf(agentStdout, "joined node %s (%s)\n", result.NodeName, result.NodeID)
	return 0
}

func runLoop(args []string) int {
	cfg := config.LoadAgent()
	flags := flag.NewFlagSet("deployer-agent run", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	serverURL := flags.String("server", cfg.ServerURL, "control plane server URL")
	credentialPath := flags.String("credential-path", cfg.NodeCredentialPath, "path to the agent credential")
	interval := flags.Duration("interval", 30*time.Second, "heartbeat interval")
	once := flags.Bool("once", false, "send one heartbeat and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(*serverURL) == "" {
		fmt.Fprintln(agentStderr, "DEPLOYER_SERVER_URL or --server is required")
		return 2
	}
	token, err := readCredential(*credentialPath)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}

	client, closeClient, err := newAgentClient(*serverURL, token)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	defer closeClient()

	logger := logging.New("deployer-agent", os.Getenv("DEPLOYER_LOG_LEVEL"))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backoff := newBackoff(initialHeartbeatBackoff, maxHeartbeatBackoff)
	connectionState := "starting"
	for {
		err := sendHeartbeat(ctx, client)
		if err == nil {
			if connectionState != "connected" {
				logger.Info("control plane connection state changed", "state", "connected")
				connectionState = "connected"
			}
			backoff.Reset()
			if *once {
				return 0
			}
			if !sleepContext(ctx, *interval) {
				return 0
			}
			continue
		}
		if strings.Contains(err.Error(), "authentication failed") || strings.Contains(err.Error(), "permission denied") {
			fmt.Fprintln(agentStderr, err)
			return 1
		}
		delay := backoff.Next()
		if connectionState != "disconnected" {
			logger.Warn("control plane connection state changed", "state", "disconnected", "error", err, "retry_in", delay.String())
			connectionState = "disconnected"
		} else {
			logger.Warn("heartbeat failed", "error", err, "retry_in", delay.String())
		}
		if *once {
			fmt.Fprintln(agentStderr, err)
			return 1
		}
		if !sleepContext(ctx, delay) {
			return 0
		}
	}
}

func sendHeartbeat(ctx context.Context, client agentClient) error {
	hostname, _ := os.Hostname()
	return client.Heartbeat(ctx, clicore.Heartbeat{
		Status:          "online",
		Hostname:        hostname,
		Arch:            runtime.GOOS + "/" + runtime.GOARCH,
		OS:              runtime.GOOS,
		Kernel:          kernelVersion(),
		ResourceSummary: resourceSummary(),
	})
}

type heartbeatBackoff struct {
	initial time.Duration
	max     time.Duration
	next    time.Duration
}

func newBackoff(initial time.Duration, max time.Duration) *heartbeatBackoff {
	return &heartbeatBackoff{initial: initial, max: max}
}

func (b *heartbeatBackoff) Next() time.Duration {
	if b.next <= 0 {
		b.next = b.initial
	}
	delay := b.next
	b.next *= 2
	if b.next > b.max {
		b.next = b.max
	}
	return delay
}

func (b *heartbeatBackoff) Reset() {
	b.next = 0
}

func kernelVersion() string {
	return cachedKernelVersion
}

func resourceSummary() string {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return fmt.Sprintf(`{"go_routines":%d,"alloc_bytes":%d}`, runtime.NumGoroutine(), memory.Alloc)
}

func detectKernelVersion() string {
	if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		return strings.TrimSpace(string(data))
	}
	output, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return runtime.GOOS
	}
	return strings.TrimSpace(string(output))
}

func writeCredential(path string, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("join response did not include an agent credential")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent credential: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict agent credential: %w", err)
	}
	return nil
}

func readCredential(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read agent credential: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("agent credential is empty")
	}
	return token, nil
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func usage() {
	fmt.Fprintln(agentStderr, "Usage: deployer-agent [--version] [--validate-config] [join|run|help]")
	fmt.Fprintln(agentStderr)
	fmt.Fprintln(agentStderr, "Commands:")
	fmt.Fprintln(agentStderr, "  join       Register this node with the control plane")
	fmt.Fprintln(agentStderr, "  run        Send periodic heartbeats")
	fmt.Fprintln(agentStderr, "  version    Print version information")
	fmt.Fprintln(agentStderr, "  help       Show help")
}
