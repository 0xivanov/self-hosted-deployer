package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

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
