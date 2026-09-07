package main

import (
	"context"
	"fmt"
	"io"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

type doctorSeverity string

const (
	doctorPass doctorSeverity = "pass"
	doctorWarn doctorSeverity = "warn"
	doctorFail doctorSeverity = "fail"
)

type doctorCheck struct {
	Name     string         `json:"name"`
	Status   doctorSeverity `json:"status"`
	Message  string         `json:"message"`
	Guidance string         `json:"guidance,omitempty"`
}

type doctorReport struct {
	Checks []doctorCheck `json:"checks"`
}

func (r doctorReport) hasFailures() bool {
	for _, check := range r.Checks {
		if check.Status == doctorFail {
			return true
		}
	}
	return false
}

func (a cliApp) doctor(args []string, opts cliOptions) int {
	hosting := len(args) == 1 && args[0] == "--hosting"
	if len(args) != 0 && !hosting {
		fmt.Fprintln(a.stderr, "usage: deployer doctor")
		return 2
	}
	resolved, err := resolveRuntimeOptions(opts)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	client, closeClient, err := a.newPlatformClient(resolved.serverURL, resolved.token)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	defer closeClient()

	report := runDoctor(context.Background(), client)
	if hosting {
		report, _ = runHostingDoctor(context.Background(), client, report)
	}
	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, report); err != nil {
			fmt.Fprintf(a.stderr, "render doctor report: %v\n", err)
			return 1
		}
	} else {
		renderDoctorReport(a.stdout, report)
	}
	if report.hasFailures() || (hosting && hostingReportHasUnresolved(report)) {
		return 1
	}
	return 0
}

// runHostingDoctor adds the checks that can be established through the
// management API. External operational gates remain warnings and are treated
// as unresolved by hosting mode, so this command cannot claim production
// readiness based on local control-plane state alone.
func runHostingDoctor(ctx context.Context, client platformClient, report doctorReport) (doctorReport, bool) {
	unresolved := false
	apps, err := client.ListApps(ctx)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:     "hosting apps",
			Status:   doctorFail,
			Message:  "cannot list applications: " + err.Error(),
			Guidance: "verify the admin token can call AppService/ListApps",
		})
	} else {
		routes, routesErr := client.ListRoutes(ctx)
		for _, app := range apps {
			if app.DesiredState.Hosting == nil {
				report.Checks = append(report.Checks, doctorCheck{
					Name:     "hosting profile: " + app.Name,
					Status:   doctorFail,
					Message:  "application has no declared hosting profile",
					Guidance: "add an explicit hosting.version and resource profile before hosting readiness review",
				})
			} else if err := app.DesiredState.Hosting.Validate(app.Replicas); err != nil {
				report.Checks = append(report.Checks, doctorCheck{
					Name:     "hosting profile: " + app.Name,
					Status:   doctorFail,
					Message:  "declared hosting profile is invalid",
					Guidance: "repair the hosting profile and redeploy the application",
				})
			} else {
				report.Checks = append(report.Checks, doctorCheck{
					Name:    "hosting profile: " + app.Name,
					Status:  doctorPass,
					Message: fmt.Sprintf("declared %s profile with replica limit %d", app.DesiredState.Hosting.Version, app.DesiredState.Hosting.MaxReplicas),
				})
			}

			status, statusErr := client.GetAppStatus(ctx, app.Name)
			if statusErr != nil {
				report.Checks = append(report.Checks, doctorCheck{
					Name:     "hosting health: " + app.Name,
					Status:   doctorFail,
					Message:  "cannot inspect application health: " + statusErr.Error(),
					Guidance: "run deployer status " + app.Name + " and inspect the latest deployment",
				})
			} else if status.RuntimeStatus != "healthy" || status.AvailableReplicas < status.DesiredReplicas {
				report.Checks = append(report.Checks, doctorCheck{
					Name:     "hosting health: " + app.Name,
					Status:   doctorFail,
					Message:  fmt.Sprintf("runtime %s with %d/%d replicas available", valueOrDash(status.RuntimeStatus), status.AvailableReplicas, status.DesiredReplicas),
					Guidance: "inspect application readiness, rollout events, and capacity",
				})
			} else {
				report.Checks = append(report.Checks, doctorCheck{
					Name:    "hosting health: " + app.Name,
					Status:  doctorPass,
					Message: fmt.Sprintf("healthy with %d/%d replicas available", status.AvailableReplicas, status.DesiredReplicas),
				})
			}

			if app.Domain == "" {
				report.Checks = append(report.Checks, doctorCheck{Name: "HTTPS route: " + app.Name, Status: doctorPass, Message: "no public route declared"})
				continue
			}
			if routesErr != nil {
				report.Checks = append(report.Checks, doctorCheck{Name: "HTTPS route: " + app.Name, Status: doctorFail, Message: "cannot inspect routes", Guidance: "verify AppService/ListRoutes and ingress health"})
				continue
			}
			route, ok := findAppRoute(routes, app)
			if !ok || !route.TLSEnabled || route.Status != "healthy" {
				message := "route is missing or not healthy HTTPS"
				if ok {
					message = fmt.Sprintf("route status %s, TLS enabled=%t", valueOrDash(route.Status), route.TLSEnabled)
				}
				report.Checks = append(report.Checks, doctorCheck{Name: "HTTPS route: " + app.Name, Status: doctorFail, Message: message, Guidance: "inspect the route, certificate, and cert-manager status"})
			} else {
				report.Checks = append(report.Checks, doctorCheck{Name: "HTTPS route: " + app.Name, Status: doctorPass, Message: "healthy route with TLS enabled"})
			}
		}
	}

	for _, gate := range []string{
		"independent backup and restore evidence",
		"real alert delivery",
		"external availability monitoring",
		"off-host audit retention",
		"fleet pin state",
		"CNI enforcement",
	} {
		report.Checks = append(report.Checks, doctorCheck{
			Name:     gate,
			Status:   doctorWarn,
			Message:  "unresolved: local doctor cannot certify this external requirement",
			Guidance: "verify the hosting operations evidence and record the result outside deployer",
		})
		unresolved = true
	}
	return report, unresolved
}

func findAppRoute(routes []clicore.RouteInfo, app clicore.AppInfo) (clicore.RouteInfo, bool) {
	for _, route := range routes {
		if route.AppID == app.ID || route.Domain == app.Domain {
			return route, true
		}
	}
	return clicore.RouteInfo{}, false
}

func hostingReportHasUnresolved(report doctorReport) bool {
	for _, check := range report.Checks {
		if check.Status == doctorWarn && check.Message == "unresolved: local doctor cannot certify this external requirement" {
			return true
		}
	}
	return false
}

func runDoctor(ctx context.Context, client platformClient) doctorReport {
	report := doctorReport{}
	status, err := client.Status(ctx)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:     "control plane",
			Status:   doctorFail,
			Message:  err.Error(),
			Guidance: "verify --server, --token, network reachability, and that deployer-server is running",
		})
		return report
	}
	report.Checks = append(report.Checks, doctorCheck{
		Name:    "control plane",
		Status:  doctorPass,
		Message: fmt.Sprintf("reachable (%s %s)", status.Version, status.Commit),
	})
	if status.Ready {
		report.Checks = append(report.Checks, doctorCheck{Name: "database and Kubernetes readiness", Status: doctorPass, Message: "server reports ready"})
	} else {
		report.Checks = append(report.Checks, doctorCheck{
			Name:     "database and Kubernetes readiness",
			Status:   doctorFail,
			Message:  "server reports not ready",
			Guidance: "check deployer-server logs, DEPLOYER_DATABASE_URL, and kubeconfig access",
		})
	}

	nodes, err := client.ListNodes(ctx)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:     "nodes",
			Status:   doctorFail,
			Message:  err.Error(),
			Guidance: "verify the admin token can call NodeService/ListNodes",
		})
		return report
	}
	report.Checks = append(report.Checks, nodeDoctorChecks(nodes)...)

	routes, err := client.ListRoutes(ctx)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:     "ingress",
			Status:   doctorWarn,
			Message:  err.Error(),
			Guidance: "deploy an app with routing or check ingress controller and cert-manager configuration",
		})
		return report
	}
	report.Checks = append(report.Checks, ingressDoctorCheck(routes))
	return report
}

func nodeDoctorChecks(nodes []clicore.NodeInfo) []doctorCheck {
	if len(nodes) == 0 {
		return []doctorCheck{{
			Name:     "nodes",
			Status:   doctorWarn,
			Message:  "no nodes are enrolled",
			Guidance: "run deployer nodes add <name>, then deployer-agent join and deployer-agent join-k3s on a worker",
		}}
	}
	readyWorkers := 0
	vpnConnected := 0
	offline := 0
	for _, node := range nodes {
		if node.Status == "offline" {
			offline++
		}
		if node.KubernetesStatus == "ready" && node.Schedulable {
			readyWorkers++
		}
		if node.VPNStatus == "connected" {
			vpnConnected++
		}
	}
	checks := []doctorCheck{}
	if readyWorkers > 0 {
		checks = append(checks, doctorCheck{
			Name:    "Kubernetes workers",
			Status:  doctorPass,
			Message: fmt.Sprintf("%d schedulable Kubernetes-ready node(s)", readyWorkers),
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:     "Kubernetes workers",
			Status:   doctorWarn,
			Message:  "no schedulable Kubernetes-ready nodes found",
			Guidance: "run deployer nodes inspect <node> and ensure deployer-agent join-k3s completed",
		})
	}
	if vpnConnected > 0 {
		checks = append(checks, doctorCheck{
			Name:    "WireGuard connectivity",
			Status:  doctorPass,
			Message: fmt.Sprintf("%d node(s) report VPN connected", vpnConnected),
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:     "WireGuard connectivity",
			Status:   doctorWarn,
			Message:  "no nodes report VPN connected",
			Guidance: "check DEPLOYER_WIREGUARD_ENDPOINT, hub public key, and deployer-agent join-k3s output",
		})
	}
	if offline > 0 {
		checks = append(checks, doctorCheck{
			Name:     "node heartbeats",
			Status:   doctorWarn,
			Message:  fmt.Sprintf("%d enrolled node(s) are offline", offline),
			Guidance: "check deployer-agent.service and network reachability on offline nodes",
		})
	} else {
		checks = append(checks, doctorCheck{Name: "node heartbeats", Status: doctorPass, Message: "no enrolled nodes are offline"})
	}
	return checks
}

func ingressDoctorCheck(routes []clicore.RouteInfo) doctorCheck {
	if len(routes) == 0 {
		return doctorCheck{
			Name:     "ingress",
			Status:   doctorWarn,
			Message:  "no routes are configured",
			Guidance: "deploy an app with routing.domain to exercise ingress",
		}
	}
	unhealthy := 0
	for _, route := range routes {
		if route.Status != "healthy" {
			unhealthy++
		}
	}
	if unhealthy == 0 {
		return doctorCheck{Name: "ingress", Status: doctorPass, Message: fmt.Sprintf("%d healthy route(s)", len(routes))}
	}
	return doctorCheck{
		Name:     "ingress",
		Status:   doctorWarn,
		Message:  fmt.Sprintf("%d route(s) are not healthy", unhealthy),
		Guidance: "run deployer routes inspect <domain> and check ingress controller/cert-manager state",
	}
}

func renderDoctorReport(w io.Writer, report doctorReport) {
	fmt.Fprintf(w, "%-8s %-30s %s\n", "STATUS", "CHECK", "MESSAGE")
	for _, check := range report.Checks {
		fmt.Fprintf(w, "%-8s %-30s %s\n", string(check.Status), check.Name, check.Message)
		if check.Guidance != "" {
			fmt.Fprintf(w, "%-8s %-30s %s\n", "", "", "next: "+check.Guidance)
		}
	}
}
