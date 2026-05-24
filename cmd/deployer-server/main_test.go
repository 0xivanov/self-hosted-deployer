package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/k3s"
)

func TestValidateConfigUsesRuntimeRequirements(t *testing.T) {
	t.Setenv("DEPLOYER_PUBLIC_BASE_URL", "")
	t.Setenv("DEPLOYER_SECRET_KEY", "")
	t.Setenv("DEPLOYER_TOKEN_HASH_KEY", "hash-key")
	t.Setenv("DEPLOYER_DATABASE_URL", "file:deployer.db")
	t.Setenv("DEPLOYER_SERVER_GRPC_ADDR", ":7443")
	t.Setenv("DEPLOYER_SERVER_HTTP_ADDR", ":7080")

	if code := run([]string{"--validate-config"}); code != 0 {
		t.Fatalf("expected validate-config to pass, got exit code %d", code)
	}
}

func TestBootstrapK3sCommandUsesInstallerPath(t *testing.T) {
	oldBootstrapper := newK3sBootstrapper
	runner := &serverFakeRunner{}
	newK3sBootstrapper = func() k3s.Bootstrapper {
		return k3s.Bootstrapper{
			Runtime: serverFakeRuntime{
				goos:     "linux",
				euid:     0,
				lookPath: exec.ErrNotFound,
			},
			Files:       &serverFakeFiles{statErr: os.ErrNotExist},
			Runner:      runner,
			HTTPClient:  serverFakeHTTPClient{body: "#!/bin/sh\n"},
			PortChecker: &serverFakePortChecker{},
		}
	}
	defer func() {
		newK3sBootstrapper = oldBootstrapper
	}()

	code := bootstrap([]string{
		"k3s",
		"--wireguard-ip", "10.8.0.1",
		"--config-path", "/tmp/k3s/config.yaml",
		"--kubeconfig", "/tmp/k3s/k3s.yaml",
		"--installer-url", "https://example.test/k3s.sh",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected installer call, got %#v", runner.calls)
	}
	if runner.calls[0].name != "/bin/sh" {
		t.Fatalf("expected installer shell, got %#v", runner.calls[0])
	}
	if !strings.Contains(strings.Join(runner.calls[0].env, "\n"), "server --config /tmp/k3s/config.yaml") {
		t.Fatalf("installer env missing config path: %#v", runner.calls[0].env)
	}
}

func TestFormatK3sAPIURLSupportsIPv6(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{
			name: "ipv4",
			host: "10.8.0.1",
			want: "https://10.8.0.1:6443",
		},
		{
			name: "ipv6",
			host: "fd00::1",
			want: "https://[fd00::1]:6443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatK3sAPIURL(tt.host); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

type serverFakeRuntime struct {
	goos     string
	euid     int
	lookPath error
}

func (r serverFakeRuntime) GOOS() string {
	return r.goos
}

func (r serverFakeRuntime) EUID() int {
	return r.euid
}

func (r serverFakeRuntime) LookPath(string) (string, error) {
	if r.lookPath != nil {
		return "", r.lookPath
	}
	return "/usr/local/bin/k3s", nil
}

type serverFakeFiles struct {
	statErr error
}

func (f *serverFakeFiles) Stat(string) (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return nil, nil
}

func (f *serverFakeFiles) MkdirAll(string, os.FileMode) error {
	return nil
}

func (f *serverFakeFiles) WriteFile(string, []byte, os.FileMode) error {
	return nil
}

type serverFakeRunner struct {
	calls []serverRunnerCall
}

type serverRunnerCall struct {
	name string
	args []string
	env  []string
}

func (r *serverFakeRunner) Run(_ context.Context, name string, args []string, env []string, _ []byte) error {
	r.calls = append(r.calls, serverRunnerCall{
		name: name,
		args: append([]string(nil), args...),
		env:  append([]string(nil), env...),
	})
	return nil
}

type serverFakeHTTPClient struct {
	body string
}

func (c serverFakeHTTPClient) Do(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(c.body)),
	}, nil
}

type serverFakePortChecker struct{}

func (*serverFakePortChecker) Check(context.Context, string, string) error {
	return nil
}
