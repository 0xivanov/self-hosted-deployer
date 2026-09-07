package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

type hostingDoctorClient struct {
	recordingClient
	apps   []clicore.AppInfo
	status map[string]clicore.AppStatusResult
	routes []clicore.RouteInfo
}

func (c hostingDoctorClient) ListApps(context.Context) ([]clicore.AppInfo, error) {
	return c.apps, nil
}

func (c hostingDoctorClient) GetAppStatus(_ context.Context, name string) (clicore.AppStatusResult, error) {
	return c.status[name], nil
}

func (c hostingDoctorClient) ListRoutes(context.Context) ([]clicore.RouteInfo, error) {
	return c.routes, nil
}

func validHostingProfile() *appconfig.HostingConfig {
	return &appconfig.HostingConfig{
		Version:     "v1",
		MaxReplicas: 3,
		Resources: appconfig.HostingResources{
			Requests: appconfig.ResourceQuantities{CPU: "100m", Memory: "128Mi", EphemeralStorage: "128Mi"},
			Limits:   appconfig.ResourceQuantities{CPU: "500m", Memory: "512Mi", EphemeralStorage: "512Mi"},
		},
	}
}

func hostingApp(profile *appconfig.HostingConfig, domain string) clicore.AppInfo {
	return clicore.AppInfo{ID: "app-1", Name: "customer-api", Replicas: 2, Domain: domain, DesiredState: appconfig.Config{Name: "customer-api", Deploy: appconfig.DeployConfig{Replicas: 2}, Hosting: profile}}
}

func hostingStatus(runtime string, available, desired int) clicore.AppStatusResult {
	return clicore.AppStatusResult{RuntimeStatus: runtime, AvailableReplicas: available, DesiredReplicas: desired}
}

func TestRunHostingDoctorHealthyProfileStillReportsExternalGates(t *testing.T) {
	app := hostingApp(validHostingProfile(), "api.example.test")
	client := hostingDoctorClient{
		apps:   []clicore.AppInfo{app},
		status: map[string]clicore.AppStatusResult{app.Name: hostingStatus("healthy", 2, 2)},
		routes: []clicore.RouteInfo{{AppID: app.ID, Domain: app.Domain, Status: "healthy", TLSEnabled: true}},
	}
	report, unresolved := runHostingDoctor(context.Background(), client, doctorReport{})
	if !unresolved || !hostingReportHasUnresolved(report) {
		t.Fatal("hosting doctor must remain unresolved for external gates")
	}
	assertHostingCheck(t, report, "hosting profile: customer-api", doctorPass)
	assertHostingCheck(t, report, "hosting health: customer-api", doctorPass)
	assertHostingCheck(t, report, "HTTPS route: customer-api", doctorPass)
	if !strings.Contains(reportText(report), "local doctor cannot certify this external requirement") {
		t.Fatal("expected explicit external gate warning")
	}
}

func TestRunHostingDoctorRejectsLegacyAppProfile(t *testing.T) {
	app := hostingApp(nil, "")
	client := hostingDoctorClient{
		apps:   []clicore.AppInfo{app},
		status: map[string]clicore.AppStatusResult{app.Name: hostingStatus("healthy", 2, 2)},
	}
	report, _ := runHostingDoctor(context.Background(), client, doctorReport{})
	assertHostingCheck(t, report, "hosting profile: customer-api", doctorFail)
	assertHostingCheck(t, report, "HTTPS route: customer-api", doctorPass)
}

func TestRunHostingDoctorRejectsMissingTLSAndUnhealthyApp(t *testing.T) {
	app := hostingApp(validHostingProfile(), "api.example.test")
	client := hostingDoctorClient{
		apps:   []clicore.AppInfo{app},
		status: map[string]clicore.AppStatusResult{app.Name: hostingStatus("degraded", 1, 2)},
		routes: []clicore.RouteInfo{{AppID: app.ID, Domain: app.Domain, Status: "healthy", TLSEnabled: false}},
	}
	report, _ := runHostingDoctor(context.Background(), client, doctorReport{})
	assertHostingCheck(t, report, "hosting health: customer-api", doctorFail)
	assertHostingCheck(t, report, "HTTPS route: customer-api", doctorFail)
}

func TestDoctorHostingExitsNonzeroForUnresolvedExternalGates(t *testing.T) {
	app := hostingApp(validHostingProfile(), "")
	client := hostingDoctorClient{
		recordingClient: recordingClient{status: clicore.ServerStatus{Version: "test", Ready: true}},
		apps:            []clicore.AppInfo{app},
		status:          map[string]clicore.AppStatusResult{app.Name: hostingStatus("healthy", 2, 2)},
	}
	var stdout, stderr bytes.Buffer
	appCLI := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	appCLI.newPlatformClient = func(string, string) (platformClient, func() error, error) {
		return client, func() error { return nil }, nil
	}
	if code := appCLI.run([]string{"--server", "localhost:7443", "--token", "dep_admin_test", "doctor", "--hosting"}); code == 0 {
		t.Fatal("hosting doctor reported readiness despite unresolved external gates")
	}
	if !strings.Contains(stdout.String(), "local doctor cannot certify this external requirement") {
		t.Fatalf("expected explicit unresolved gate output, got %q (stderr %q)", stdout.String(), stderr.String())
	}
}

func assertHostingCheck(t *testing.T, report doctorReport, name string, want doctorSeverity) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != want {
				t.Fatalf("check %q status=%s, want %s (%s)", name, check.Status, want, check.Message)
			}
			return
		}
	}
	t.Fatalf("missing check %q in %#v", name, report.Checks)
}

func reportText(report doctorReport) string {
	var builder strings.Builder
	for _, check := range report.Checks {
		builder.WriteString(check.Name)
		builder.WriteString(" ")
		builder.WriteString(check.Message)
	}
	return builder.String()
}
