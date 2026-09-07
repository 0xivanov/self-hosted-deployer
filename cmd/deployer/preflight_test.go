package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type recordingPreflightClient struct {
	recordingAppClient
	calls int
	err   error
}

func (c *recordingPreflightClient) PreflightApp(context.Context, string) (clicore.PreflightResult, error) {
	c.calls++
	return clicore.PreflightResult{DesiredState: "{}", Warnings: []string{"snapshot only"}}, c.err
}
func TestPreflightDispatchNeverFallsBackToDeploy(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "old server"}[unavailable], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.yaml")
			if err := os.WriteFile(path, []byte(testDeployYAML("example/api:v1")), 0600); err != nil {
				t.Fatal(err)
			}
			client := &recordingPreflightClient{}
			if unavailable {
				client.err = status.Error(codes.Unimplemented, "unknown method PreflightApp")
			}
			var out, stderr bytes.Buffer
			app := newCLIApp(strings.NewReader(""), &out, &stderr)
			app.newPlatformClient = func(string, string) (platformClient, func() error, error) {
				return client, func() error { return nil }, nil
			}
			code := app.run([]string{"--server", "localhost:7443", "--token", "test", "preflight", "-f", path})
			if (code != 0) != unavailable || client.calls != 1 || client.deployerYAML != "" {
				t.Fatalf("code=%d calls=%d deploy=%q stderr=%s", code, client.calls, client.deployerYAML, stderr.String())
			}
			if !unavailable && !strings.Contains(out.String(), "read-only") {
				t.Fatal(out.String())
			}
		})
	}
}
