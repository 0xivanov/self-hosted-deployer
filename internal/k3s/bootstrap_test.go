package k3s

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestServerConfigYAMLIncludesWireGuardAPISettings(t *testing.T) {
	data, err := ServerConfigYAML(Config{
		WireGuardIP:    "10.8.0.1",
		ConfigPath:     "/tmp/k3s/config.yaml",
		KubeconfigPath: "/tmp/k3s/k3s.yaml",
	})
	if err != nil {
		t.Fatalf("render config: %v", err)
	}

	rendered := string(data)
	for _, want := range []string{
		"bind-address: 10.8.0.1",
		"advertise-address: 10.8.0.1",
		"node-ip: 10.8.0.1",
		"- 10.8.0.1",
		"write-kubeconfig: /tmp/k3s/k3s.yaml",
		"write-kubeconfig-mode: \"0644\"",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected config to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestBootstrapFailsSafelyWhenK3sAlreadyExists(t *testing.T) {
	runner := &fakeRunner{}
	bootstrapper := Bootstrapper{
		Runtime: fakeRuntime{
			goos:     "linux",
			euid:     0,
			k3sPath:  "/usr/local/bin/k3s",
			lookPath: nil,
		},
		Files:       &fakeFiles{statErr: os.ErrNotExist},
		Runner:      runner,
		HTTPClient:  fakeHTTPClient{body: "ignored"},
		PortChecker: &fakePortChecker{},
	}

	result, err := bootstrapper.Bootstrap(context.Background(), Config{WireGuardIP: "10.8.0.1"})
	if err == nil || !strings.Contains(err.Error(), "already appears installed") {
		t.Fatalf("expected existing install error, got %v", err)
	}
	if !result.ExistingInstall {
		t.Fatal("expected result to mark existing install")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("installer should not run without force, got %#v", runner.calls)
	}
}

func TestBootstrapWritesConfigAndRunsInstaller(t *testing.T) {
	files := &fakeFiles{statErr: os.ErrNotExist}
	runner := &fakeRunner{}
	ports := &fakePortChecker{}
	bootstrapper := Bootstrapper{
		Runtime: fakeRuntime{
			goos:     "linux",
			euid:     0,
			lookPath: exec.ErrNotFound,
		},
		Files:       files,
		Runner:      runner,
		HTTPClient:  fakeHTTPClient{body: "#!/bin/sh\n"},
		PortChecker: ports,
	}

	result, err := bootstrapper.Bootstrap(context.Background(), Config{
		WireGuardIP:    "10.8.0.1",
		ConfigPath:     "/tmp/rancher/k3s/config.yaml",
		KubeconfigPath: "/tmp/rancher/k3s/k3s.yaml",
		InstallerURL:   "https://example.test/k3s.sh",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if result.ExistingInstall {
		t.Fatal("did not expect existing install")
	}
	if files.mkdirPath != "/tmp/rancher/k3s" {
		t.Fatalf("expected config directory mkdir, got %q", files.mkdirPath)
	}
	if files.writePath != "/tmp/rancher/k3s/config.yaml" || files.writePerm != 0o600 {
		t.Fatalf("unexpected config write path/perm: %s %o", files.writePath, files.writePerm)
	}
	if !strings.Contains(string(files.writeData), "advertise-address: 10.8.0.1") {
		t.Fatalf("config missing advertise address:\n%s", string(files.writeData))
	}
	if len(ports.calls) != 2 || ports.calls[0] != "10.8.0.1:0" || ports.calls[1] != "10.8.0.1:6443" {
		t.Fatalf("unexpected port checks: %#v", ports.calls)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected installer call, got %#v", runner.calls)
	}
	if runner.calls[0].name != "/bin/sh" || !strings.Contains(runner.calls[0].env[0], "server --config /tmp/rancher/k3s/config.yaml") {
		t.Fatalf("unexpected installer call: %#v", runner.calls[0])
	}
	if string(runner.calls[0].stdin) != "#!/bin/sh\n" {
		t.Fatalf("installer stdin was not passed through: %q", string(runner.calls[0].stdin))
	}
}

func TestBootstrapRequiresLinuxRoot(t *testing.T) {
	tests := []struct {
		name    string
		runtime fakeRuntime
		want    string
	}{
		{
			name:    "non linux",
			runtime: fakeRuntime{goos: "darwin", euid: 0, lookPath: exec.ErrNotFound},
			want:    "requires Linux",
		},
		{
			name:    "non root",
			runtime: fakeRuntime{goos: "linux", euid: 501, lookPath: exec.ErrNotFound},
			want:    "must run as root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bootstrapper := Bootstrapper{
				Runtime:     tt.runtime,
				Files:       &fakeFiles{statErr: os.ErrNotExist},
				Runner:      &fakeRunner{},
				HTTPClient:  fakeHTTPClient{body: "ignored"},
				PortChecker: &fakePortChecker{},
			}
			_, err := bootstrapper.Bootstrap(context.Background(), Config{WireGuardIP: "10.8.0.1"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

type fakeRuntime struct {
	goos     string
	euid     int
	k3sPath  string
	lookPath error
}

func (r fakeRuntime) GOOS() string {
	return r.goos
}

func (r fakeRuntime) EUID() int {
	return r.euid
}

func (r fakeRuntime) LookPath(string) (string, error) {
	if r.lookPath != nil {
		return "", r.lookPath
	}
	return r.k3sPath, nil
}

type fakeFiles struct {
	statErr   error
	mkdirPath string
	writePath string
	writeData []byte
	writePerm os.FileMode
}

func (f *fakeFiles) Stat(string) (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return nil, nil
}

func (f *fakeFiles) MkdirAll(path string, _ os.FileMode) error {
	f.mkdirPath = path
	return nil
}

func (f *fakeFiles) WriteFile(path string, data []byte, perm os.FileMode) error {
	f.writePath = path
	f.writeData = append([]byte(nil), data...)
	f.writePerm = perm
	return nil
}

type fakeRunner struct {
	calls []runnerCall
}

type runnerCall struct {
	name  string
	args  []string
	env   []string
	stdin []byte
}

func (r *fakeRunner) Run(_ context.Context, name string, args []string, env []string, stdin []byte) error {
	r.calls = append(r.calls, runnerCall{
		name:  name,
		args:  append([]string(nil), args...),
		env:   append([]string(nil), env...),
		stdin: append([]byte(nil), stdin...),
	})
	return nil
}

type fakeHTTPClient struct {
	body string
	err  error
}

func (c fakeHTTPClient) Do(*http.Request) (*http.Response, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(c.body)),
	}, nil
}

type fakePortChecker struct {
	calls []string
	err   error
}

func (c *fakePortChecker) Check(_ context.Context, host string, port string) error {
	if c.err != nil {
		return c.err
	}
	c.calls = append(c.calls, host+":"+port)
	return nil
}
