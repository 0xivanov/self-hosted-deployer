package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/logging"
)

func runLoop(args []string) int {
	cfg := config.LoadAgent()
	flags := flag.NewFlagSet("deployer-agent run", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	serverURL := flags.String("server", cfg.ServerURL, "control plane server URL")
	credentialPath := flags.String("credential-path", cfg.NodeCredentialPath, "path to the agent credential")
	interval := flags.Duration("interval", 30*time.Second, "heartbeat interval")
	once := flags.Bool("once", false, "send one heartbeat and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(*serverURL) == "" {
		fmt.Fprintln(agentStderr, "DEPLOYER_SERVER_URL or --server is required")
		return 2
	}
	token, err := readCredential(*credentialPath)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}

	client, closeClient, err := newAgentClient(*serverURL, token)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	defer closeClient()

	logger := logging.New("deployer-agent", os.Getenv("DEPLOYER_LOG_LEVEL"))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backoff := newBackoff(initialHeartbeatBackoff, maxHeartbeatBackoff)
	connectionState := "starting"
	for {
		err := sendHeartbeat(ctx, client)
		if err == nil {
			if connectionState != "connected" {
				logger.Info("control plane connection state changed", "state", "connected")
				connectionState = "connected"
			}
			backoff.Reset()
			if *once {
				return 0
			}
			if !sleepContext(ctx, *interval) {
				return 0
			}
			continue
		}
		if strings.Contains(err.Error(), "authentication failed") || strings.Contains(err.Error(), "permission denied") {
			fmt.Fprintln(agentStderr, err)
			return 1
		}
		delay := backoff.Next()
		if connectionState != "disconnected" {
			logger.Warn("control plane connection state changed", "state", "disconnected", "error", err, "retry_in", delay.String())
			connectionState = "disconnected"
		} else {
			logger.Warn("heartbeat failed", "error", err, "retry_in", delay.String())
		}
		if *once {
			fmt.Fprintln(agentStderr, err)
			return 1
		}
		if !sleepContext(ctx, delay) {
			return 0
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
