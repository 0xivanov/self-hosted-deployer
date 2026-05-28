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
	if len(args) != 0 {
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
	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, report); err != nil {
			fmt.Fprintf(a.stderr, "render doctor report: %v\n", err)
			return 1
		}
	} else {
		renderDoctorReport(a.stdout, report)
	}
	if report.hasFailures() {
		return 1
	}
	return 0
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
