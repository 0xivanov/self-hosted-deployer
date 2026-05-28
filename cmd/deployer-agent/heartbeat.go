package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"github.com/0xivanov/self-hosted-deployer/internal/config"
)

func sendHeartbeat(ctx context.Context, client agentClient) error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("detect hostname: %w", err)
	}
	return client.Heartbeat(ctx, clicore.Heartbeat{
		Status:          "online",
		Hostname:        hostname,
		Arch:            runtime.GOOS + "/" + runtime.GOARCH,
		OS:              runtime.GOOS,
		Kernel:          kernelVersion(),
		ResourceSummary: resourceSummary(),
		VPNStatus:       vpnConnectivityStatus(ctx, config.LoadAgent().WireGuardHubIP),
	})
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
