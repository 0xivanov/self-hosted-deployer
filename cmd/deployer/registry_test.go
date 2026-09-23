package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

type fakeRegistryClient struct {
	created   int
	createErr error
	listed    []clicore.RegistryCredentialInfo
	gotUser   string
	gotPass   string
}

func (f *fakeRegistryClient) CreateRegistryCredential(_ context.Context, app, revision, registry, username, password string) (clicore.RegistryCredentialInfo, error) {
	f.created++
	f.gotUser, f.gotPass = username, password
	if f.createErr != nil {
		return clicore.RegistryCredentialInfo{}, f.createErr
	}
	return clicore.RegistryCredentialInfo{AppName: app, Revision: revision, Registry: registry}, nil
}

func (f *fakeRegistryClient) ListRegistryCredentials(_ context.Context, _ string) ([]clicore.RegistryCredentialInfo, error) {
	return f.listed, nil
}

func TestRegistryCreateReadsOwnerPrivateFileWithoutPrintingSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"username":"owner","password":"super-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := cliApp{stdout: &stdout, stderr: &stderr}
	client := &fakeRegistryClient{}
	revision := strings.Repeat("a", 64)
	code := app.registryCreate([]string{"--credentials", path, "my-api", revision, "ghcr.io"}, runtimeOptions{output: clicore.OutputHuman}, client)
	if code != 0 || client.created != 1 || client.gotPass != "super-secret" {
		t.Fatalf("create failed: code=%d client=%#v stderr=%q", code, client, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "super-secret") {
		t.Fatal("credential password was printed")
	}
}

func TestRegistryCreateRejectsInvalidTargetBeforeReadingOrMutation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := cliApp{stdout: &stdout, stderr: &stderr}
	client := &fakeRegistryClient{}
	code := app.registryCreate([]string{"--credentials", "/does/not/exist", "my-api", "bad", "docker.io"}, runtimeOptions{}, client)
	if code == 0 || client.created != 0 {
		t.Fatalf("invalid target reached mutation: code=%d client=%#v", code, client)
	}
}

func TestRegistryCredentialFileRejectsUnknownAndTrailingData(t *testing.T) {
	for name, contents := range map[string]string{
		"unknown":  `{"username":"u","password":"p","extra":"x"}`,
		"trailing": `{"username":"u","password":"p"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRegistryCredentialFile(path); err == nil {
				t.Fatal("expected strict JSON rejection")
			}
		})
	}
}

func TestRegistryCreateRejectsInsecureCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"username":"u","password":"p"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistryCredentialFile(path); err == nil {
		t.Fatal("expected insecure permission rejection")
	}
}

func TestRegistryCredentialFileRejectsSymlinkAndOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"username":"u","password":"p"}`), 0600); err != nil {
		t.Fatal(err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistryCredentialFile(link); err == nil {
		t.Fatal("accepted symlink")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", maxRegistryCredentialFileSize+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistryCredentialFile(path); err == nil {
		t.Fatal("accepted oversized file")
	}
}
func TestRegistryCreateDoesNotPrintUpstreamCredentialError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"username":"u","password":"private-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	app := cliApp{stdout: &out, stderr: &stderr}
	client := &fakeRegistryClient{createErr: errors.New("private-token upstream error")}
	code := app.registryCreate([]string{"--credentials", path, "my-api", strings.Repeat("a", 64), "ghcr.io"}, runtimeOptions{}, client)
	if code != 1 || strings.Contains(out.String()+stderr.String(), "private-token") {
		t.Fatal("upstream credential error was printed or success reported")
	}
}
