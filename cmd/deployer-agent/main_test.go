package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"github.com/0xivanov/self-hosted-deployer/internal/k3s"
	"github.com/0xivanov/self-hosted-deployer/internal/wireguard"
)

func TestJoinPersistsCredentialWithoutPrintingToken(t *testing.T) {
	credentialPath := filepath.Join(t.TempDir(), "agent", "token")
	privateKeyPath := filepath.Join(t.TempDir(), "wireguard", "privatekey")
	fake := &fakeAgentClient{
		joinResult: clicore.JoinResult{
			NodeID:      "node-1",
			NodeName:    "pi-kitchen",
			WireGuardIP: "10.8.0.2",
			AgentToken:  "dep_agent_secret",
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	restore := replaceAgentGlobals(&stdout, &stderr, fake)
	defer restore()

	code := join([]string{
		"--server", "localhost:7443",
		"--token", "dep_join_once",
		"--credential-path", credentialPath,
		"--wireguard-private-key-path", privateKeyPath,
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s", code, stderr.String())
	}
	if fake.serverURL != "localhost:7443" || fake.clientToken != "" {
		t.Fatalf("unexpected client setup: server=%q token=%q", fake.serverURL, fake.clientToken)
	}
	if fake.joinToken != "dep_join_once" {
		t.Fatalf("expected join token to be sent, got %q", fake.joinToken)
	}
	if err := wireguard.ValidatePublicKey(fake.publicKey); err != nil {
		t.Fatalf("expected generated public key to be sent: %v", err)
	}

	data, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatalf("read credential: %v", err)
	}
	if string(data) != "dep_agent_secret\n" {
		t.Fatalf("unexpected credential contents: %q", string(data))
	}
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatalf("stat credential: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected credential mode 0600, got %o", got)
	}
	privateKeyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatalf("read WireGuard private key: %v", err)
	}
	if err := wireguard.ValidatePrivateKey(strings.TrimSpace(string(privateKeyData))); err != nil {
		t.Fatalf("expected stored WireGuard private key: %v", err)
	}
	privateKeyInfo, err := os.Stat(privateKeyPath)
	if err != nil {
		t.Fatalf("stat WireGuard private key: %v", err)
	}
	if got := privateKeyInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected WireGuard private key mode 0600, got %o", got)
	}
	privateKey := strings.TrimSpace(string(privateKeyData))
	if strings.Contains(stdout.String(), "dep_agent_secret") ||
		strings.Contains(stderr.String(), "dep_agent_secret") ||
		strings.Contains(stdout.String(), privateKey) ||
		strings.Contains(stderr.String(), privateKey) {
		t.Fatalf("agent token and WireGuard private key should not be printed, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunOnceSendsHeartbeatWithStoredCredential(t *testing.T) {
	credentialPath := filepath.Join(t.TempDir(), "agent", "token")
	if err := writeCredential(credentialPath, "dep_agent_saved"); err != nil {
		t.Fatalf("write credential: %v", err)
	}

	fake := &fakeAgentClient{}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	restore := replaceAgentGlobals(&stdout, &stderr, fake)
	defer restore()

	code := runLoop([]string{
		"--server", "localhost:7443",
		"--credential-path", credentialPath,
		"--once",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s", code, stderr.String())
	}
	if fake.clientToken != "dep_agent_saved" {
		t.Fatalf("expected stored credential to be used, got %q", fake.clientToken)
	}
	if fake.heartbeat.Status != "online" || fake.heartbeat.Hostname == "" || fake.heartbeat.Arch == "" || fake.heartbeat.OS == "" {
		t.Fatalf("heartbeat missing required metadata: %#v", fake.heartbeat)
	}
	if fake.heartbeat.Kernel == "" {
		t.Fatal("expected heartbeat kernel metadata")
	}
	if !strings.Contains(fake.heartbeat.ResourceSummary, "go_routines") {
		t.Fatalf("expected resource summary, got %q", fake.heartbeat.ResourceSummary)
	}
}

func TestRunStopsClearlyOnBadCredentials(t *testing.T) {
	credentialPath := filepath.Join(t.TempDir(), "agent", "token")
	if err := writeCredential(credentialPath, "dep_agent_bad"); err != nil {
		t.Fatalf("write credential: %v", err)
	}

	fake := &fakeAgentClient{heartbeatErr: errors.New("authentication failed: invalid bearer token")}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	restore := replaceAgentGlobals(&stdout, &stderr, fake)
	defer restore()

	code := runLoop([]string{
		"--server", "localhost:7443",
		"--credential-path", credentialPath,
		"--once",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "authentication failed") {
		t.Fatalf("expected clear auth error, got %q", stderr.String())
	}
}

func TestJoinK3sConnectsWireGuardAndRunsWorkerBootstrap(t *testing.T) {
	credentialPath := filepath.Join(t.TempDir(), "agent", "token")
	privateKeyPath := filepath.Join(t.TempDir(), "wireguard", "privatekey")
	configPath := filepath.Join(t.TempDir(), "wireguard", "wg0.conf")
	if err := writeCredential(credentialPath, "dep_agent_saved"); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	if err := writeWireGuardPrivateKey(privateKeyPath, "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="); err != nil {
		t.Fatalf("write WireGuard key: %v", err)
	}
	fake := &fakeAgentClient{bootstrap: clicore.WorkerBootstrap{
		NodeName: "pi-kitchen", WireGuardIP: "10.8.0.2", WireGuardSubnet: "10.8.0.0/24",
		WireGuardHubIP: "10.8.0.1", WireGuardHubPublicKey: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=",
		WireGuardEndpoint: "deploy.example.com:51820", K3sURL: "https://10.8.0.1:6443", K3sToken: "worker-token",
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	restore := replaceAgentGlobals(&stdout, &stderr, fake)
	defer restore()
	var commands [][]string
	runAgentCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, append([]string{name}, args...))
		return []byte("up"), nil
	}
	files := &agentTestFiles{}
	runner := &agentTestRunner{}
	newAgentK3sBootstrapper = func() k3s.Bootstrapper {
		return k3s.Bootstrapper{
			Runtime: agentTestRuntime{}, Files: files, Runner: runner,
			HTTPClient: agentTestHTTPClient{},
		}
	}
	code := joinK3s([]string{
		"--server", "localhost:7443", "--credential-path", credentialPath,
		"--wireguard-private-key-path", privateKeyPath, "--wireguard-config-path", configPath,
		"--k3s-config-path", "/tmp/k3s/config.yaml",
	})
	if code != 0 {
		t.Fatalf("expected join-k3s success, got %d: %s", code, stderr.String())
	}
	wireGuardData, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(wireGuardData), "PersistentKeepalive = 25") {
		t.Fatalf("expected WireGuard config: data=%q err=%v", wireGuardData, err)
	}
	if len(commands) != 1 || commands[0][0] != "wg" || len(runner.calls) != 1 ||
		!strings.Contains(strings.Join(runner.calls[0].env, "\n"), "K3S_TOKEN=worker-token") {
		t.Fatalf("unexpected setup commands=%#v runner=%#v", commands, runner.calls)
	}
	if strings.Contains(stdout.String(), "worker-token") || strings.Contains(stderr.String(), "worker-token") {
		t.Fatalf("worker token must not be printed: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestHeartbeatBackoffSequenceAndReset(t *testing.T) {
	backoff := newBackoff(time.Second, 5*time.Second)
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		if got := backoff.Next(); got != want {
			t.Fatalf("got delay %s, want %s", got, want)
		}
	}
	backoff.Reset()
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("reset delay got %s, want 1s", got)
	}
}

func replaceAgentGlobals(stdout *bytes.Buffer, stderr *bytes.Buffer, fake *fakeAgentClient) func() {
	oldStdout := agentStdout
	oldStderr := agentStderr
	oldClient := newAgentClient
	oldCommand := runAgentCommand
	oldCheck := checkVPNConnectivity
	oldBootstrapper := newAgentK3sBootstrapper
	agentStdout = stdout
	agentStderr = stderr
	newAgentClient = func(serverURL string, token string) (agentClient, func() error, error) {
		fake.serverURL = serverURL
		fake.clientToken = token
		return fake, func() error {
			fake.closed = true
			return nil
		}, nil
	}
	checkVPNConnectivity = func(context.Context, string) error { return nil }
	return func() {
		agentStdout = oldStdout
		agentStderr = oldStderr
		newAgentClient = oldClient
		runAgentCommand = oldCommand
		checkVPNConnectivity = oldCheck
		newAgentK3sBootstrapper = oldBootstrapper
	}
}

type fakeAgentClient struct {
	serverURL    string
	clientToken  string
	closed       bool
	joinToken    string
	publicKey    string
	joinResult   clicore.JoinResult
	joinErr      error
	heartbeat    clicore.Heartbeat
	heartbeatErr error
	bootstrap    clicore.WorkerBootstrap
}

func (c *fakeAgentClient) JoinNode(_ context.Context, joinToken string, _ string, _ string, publicKey string) (clicore.JoinResult, error) {
	c.joinToken = joinToken
	c.publicKey = publicKey
	if c.joinErr != nil {
		return clicore.JoinResult{}, c.joinErr
	}
	return c.joinResult, nil
}

func (c *fakeAgentClient) Heartbeat(_ context.Context, heartbeat clicore.Heartbeat) error {
	c.heartbeat = heartbeat
	return c.heartbeatErr
}

func (c *fakeAgentClient) GetWorkerBootstrap(context.Context) (clicore.WorkerBootstrap, error) {
	return c.bootstrap, nil
}

type agentTestRuntime struct{}

func (agentTestRuntime) GOOS() string                    { return "linux" }
func (agentTestRuntime) EUID() int                       { return 0 }
func (agentTestRuntime) LookPath(string) (string, error) { return "", exec.ErrNotFound }

type agentTestFiles struct {
	data []byte
}

func (f *agentTestFiles) Stat(string) (os.FileInfo, error)   { return nil, os.ErrNotExist }
func (f *agentTestFiles) MkdirAll(string, os.FileMode) error { return nil }
func (f *agentTestFiles) WriteFile(_ string, data []byte, _ os.FileMode) error {
	f.data = data
	return nil
}

type agentTestRunner struct {
	calls []agentRunnerCall
}

type agentRunnerCall struct {
	env []string
}

func (r *agentTestRunner) Run(_ context.Context, _ string, _ []string, env []string, _ []byte) error {
	r.calls = append(r.calls, agentRunnerCall{env: append([]string(nil), env...)})
	return nil
}

type agentTestHTTPClient struct{}

func (agentTestHTTPClient) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("#!/bin/sh\n"))}, nil
}
